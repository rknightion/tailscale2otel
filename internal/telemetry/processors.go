package telemetry

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

// Metric readers and log/span processors — everything between an SDK provider
// and its exporter. Split out of provider.go so queue/batch/immediacy concerns
// live in one file with one owner.

// noopReservoir is an exemplar.Reservoir that never stores anything.
// It is used to suppress per-series reservoir allocations for synchronous
// Counter, UpDownCounter, and Gauge instruments when tracing is enabled.
// Those instruments are always recorded with context.Background() in this
// app, so their default FixedSizeReservoir (sized to GOMAXPROCS) would be
// allocated per unique time series and never populated — pure dead-weight heap.
type noopReservoir struct{}

func (noopReservoir) Offer(_ context.Context, _ time.Time, _ exemplar.Value, _ []attribute.KeyValue) {
}
func (noopReservoir) Collect(_ *[]exemplar.Exemplar) {}

// noopReservoirSingleton is the single instance reused across all series.
// Because noopReservoir holds no state, sharing it is safe.
var noopReservoirSingleton noopReservoir

// noopReservoirProvider returns the no-op singleton for any attribute set,
// so there is zero per-series allocation.
func noopReservoirProvider(_ attribute.Set) exemplar.Reservoir {
	return noopReservoirSingleton
}

// noopExemplarSelector returns noopReservoirProvider for any aggregation.
// It is used as the ExemplarReservoirProviderSelector on the per-kind views
// that suppress exemplars for synchronous non-histogram instruments.
func noopExemplarSelector(_ sdkmetric.Aggregation) exemplar.ReservoirProvider {
	return noopReservoirProvider
}

// metricProviderOptions returns the MeterProvider options shared by the production
// pipeline and tests — everything except the reader, which differs (a PeriodicReader
// in production, a ManualReader in tests). Centralizing them here lets the
// cardinality-limit and exemplar-filter behavior be asserted against an in-memory
// reader without duplicating the wiring.
//
// Exemplar strategy:
//   - tracingEnabled=false: AlwaysOffFilter — no reservoirs allocated anywhere.
//   - tracingEnabled=true: TraceBasedFilter globally, BUT three per-instrument-kind
//     Views override the reservoir provider for synchronous Counter, UpDownCounter,
//     and Gauge to a no-op singleton. Those instruments are always recorded with
//     context.Background() in this app (via the Emitter's Counter/Gauge/
//     UpDownCounter methods), so their default FixedSizeReservoir (sized to
//     GOMAXPROCS) would be allocated per unique time series and can never be
//     populated — pure dead-weight heap at high cardinality (thousands of
//     flow-metric series). Only Float64Histogram (e.g. api.duration, recorded via
//     HistogramCtx with a real span context) keeps the default reservoir so trace
//     exemplar linking still works for that instrument. Observable (async)
//     instruments are already dropped by the SDK under TraceBasedFilter, so no
//     views are needed for them.
func metricProviderOptions(res *resource.Resource, cardinalityLimit int, tracingEnabled bool) []sdkmetric.Option {
	// With a TracerProvider present, use the trace-based exemplar filter so the
	// api.duration histogram (and other ctx-aware records) link to sampled spans.
	// Without tracing, keep exemplars OFF: the trace-based filter would allocate a
	// reservoir per series that can never be populated (no spans) yet is still
	// walked and serialized on every export — pure dead-weight alloc/CPU.
	exemplarFilter := exemplar.AlwaysOffFilter
	if tracingEnabled {
		exemplarFilter = exemplar.TraceBasedFilter
	}
	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		// Hard per-instrument cardinality limit (0/neg = unlimited). Raises the SDK
		// default of 2000 to whatever the app configures (default 10000); beyond it
		// the SDK emits otel_metric_overflow.
		sdkmetric.WithCardinalityLimit(cardinalityLimit),
		sdkmetric.WithExemplarFilter(exemplarFilter),
	}
	if tracingEnabled {
		// Suppress exemplar reservoirs for every synchronous non-histogram kind.
		// A wildcard Name:"*" with an explicit Kind matches all instruments of that
		// kind. mask.Name must stay empty (no rename) when using wildcards.
		// Histograms are intentionally omitted — they keep the default aligned-bucket
		// reservoir so api.duration exemplars link to sampled traces.
		noopMask := sdkmetric.Stream{ExemplarReservoirProviderSelector: noopExemplarSelector}
		opts = append(opts,
			sdkmetric.WithView(
				sdkmetric.NewView(sdkmetric.Instrument{Name: "*", Kind: sdkmetric.InstrumentKindCounter}, noopMask),
				sdkmetric.NewView(sdkmetric.Instrument{Name: "*", Kind: sdkmetric.InstrumentKindUpDownCounter}, noopMask),
				sdkmetric.NewView(sdkmetric.Instrument{Name: "*", Kind: sdkmetric.InstrumentKindGauge}, noopMask),
			),
		)
	}
	return opts
}

// lockedWriter serializes concurrent writes to an underlying writer. The stdout
// metric, log and trace exporters all share one destination (opts.StdoutWriter,
// or os.Stdout when unset) and write to it concurrently: during normal operation
// each exporter flushes on its own independent schedule, and since #204 the
// Provider's metric/log/trace Shutdowns run concurrently too. Without a shared
// lock their JSON records interleave on os.Stdout (and a test *bytes.Buffer
// data-races). Wrapping the shared writer once in NewProvider gives all three
// exporters the same mutex.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// defaultMetricInterval is the PeriodicReader interval used when Options leaves
// MetricInterval unset.
const defaultMetricInterval = 60 * time.Second

// stdoutDefaultMetricInterval is the PeriodicReader interval used for the
// "stdout" protocol when neither Options.MetricInterval nor
// Options.Stdout.MetricInterval is set (#384). Short enough to see output
// promptly at a terminal without spamming it every collection; a long way
// short of defaultMetricInterval's 60s, which is what made stdout unusable as
// an immediate debugging sink.
const stdoutDefaultMetricInterval = 5 * time.Second

// newMetricReader builds the OTLP metric reader for opts. It is the single seam
// NewProvider uses, so reader policy (interval, export batching, per-protocol
// immediacy, export timeout) is decided here rather than at the call site.
//
// An explicit Options.MetricInterval always wins regardless of protocol. When
// unset, the stdout protocol gets a short default (Options.Stdout.MetricInterval,
// or stdoutDefaultMetricInterval) instead of the production 60s default (#384) —
// stdout is documented as a local debugging sink, so waiting a full minute to see
// a metric defeats the point of it.
func newMetricReader(exp sdkmetric.Exporter, opts Options) (*sdkmetric.PeriodicReader, error) {
	interval := opts.MetricInterval
	if interval <= 0 {
		if opts.Protocol == "stdout" {
			interval = opts.Stdout.MetricInterval
			if interval <= 0 {
				interval = stdoutDefaultMetricInterval
			}
		} else {
			interval = defaultMetricInterval
		}
	}
	return newPeriodicMetricReader(exp, interval, opts.MetricExportBatchSize)
}

// newLogProcessor builds the log processor for opts. It is the single seam
// NewProvider uses, so queue/batch/immediacy policy is decided here.
//
//   - Protocol == "stdout" (#384): sdklog.NewSimpleProcessor exports each record
//     synchronously and inline — there is no "batched stdout" mode, since stdout
//     carries no production reliability promise anyway (StdoutOptions doc comment).
//   - opts.Batch.Tracker == nil (the default, #358): the plain
//     sdklog.NewBatchProcessor(exp) — with zero QueueOptions this appends NO
//     functional options at all, so it is the exact same call this function made
//     before #358 existed.
//   - opts.Batch.Tracker != nil: this package's own queueingLogProcessor, which
//     the SDK's unexported BatchProcessor cannot provide (no queue-size or
//     dropped-count accessor — see the #358 report), registered with the tracker
//     so Report can emit real occupancy/drop telemetry for it.
func newLogProcessor(exp sdklog.Exporter, opts Options) sdklog.Processor {
	if opts.Protocol == "stdout" {
		return sdklog.NewSimpleProcessor(exp)
	}
	if opts.Batch.Tracker == nil {
		return sdklog.NewBatchProcessor(exp, opts.Batch.Logs.sdkLogOptions()...)
	}
	p := newQueueingLogProcessor(exp, opts.Batch.Logs.resolve(dfltLogQueueConfig))
	opts.Batch.Tracker.register(SignalLogs, p.q)
	return p
}

// newSpanProcessor builds the span processor for opts. It is the single seam
// NewProvider uses, so queue/batch/immediacy policy is decided here. Mirrors
// newLogProcessor's three-way split; see its doc comment for the rationale.
func newSpanProcessor(exp sdktrace.SpanExporter, opts Options) sdktrace.SpanProcessor {
	if opts.Protocol == "stdout" {
		return sdktrace.NewSimpleSpanProcessor(exp)
	}
	if opts.Batch.Tracker == nil {
		return sdktrace.NewBatchSpanProcessor(exp, opts.Batch.Traces.sdkSpanOptions()...)
	}
	p := newQueueingSpanProcessor(exp, opts.Batch.Traces.resolve(dfltTraceQueueConfig))
	opts.Batch.Tracker.register(SignalTraces, p.q)
	return p
}

const metricExportBatchSizeEnv = "OTEL_GO_X_METRIC_EXPORT_BATCH_SIZE"

// newPeriodicMetricReader configures otel-go's pinned metric export batching
// feature for reader construction, then restores the process environment. The
// SDK reads the feature once in NewPeriodicReader, so the setting does not need
// to leak into unrelated readers constructed later in the process.
func newPeriodicMetricReader(exp sdkmetric.Exporter, interval time.Duration, batchSize int) (*sdkmetric.PeriodicReader, error) {
	if batchSize <= 0 {
		return sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(interval)), nil
	}

	previous, existed := os.LookupEnv(metricExportBatchSizeEnv)
	if err := os.Setenv(metricExportBatchSizeEnv, strconv.Itoa(batchSize)); err != nil {
		return nil, fmt.Errorf("configure metric export batch size: %w", err)
	}
	reader := sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(interval))

	var err error
	if existed {
		err = os.Setenv(metricExportBatchSizeEnv, previous)
	} else {
		err = os.Unsetenv(metricExportBatchSizeEnv)
	}
	if err != nil {
		return nil, fmt.Errorf("restore metric export batch size environment: %w", err)
	}
	return reader, nil
}

// constAttrSpanProcessor stamps provider-scoped const attrs (tailnet/provider) on
// every span at start, replacing the Resource attributes item L removed.
type constAttrSpanProcessor struct{ attrs []attribute.KeyValue }

func (p constAttrSpanProcessor) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	s.SetAttributes(p.attrs...)
}
func (constAttrSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (constAttrSpanProcessor) Shutdown(context.Context) error   { return nil }
func (constAttrSpanProcessor) ForceFlush(context.Context) error { return nil }

// QueueOptions tunes one signal's (logs or traces) batch processor queue
// (#358): capacity, export batch size, export interval, and export timeout.
// Zero value leaves every field unset, so the underlying SDK's own default
// applies for that field — and the defaults differ between signals (export
// interval especially: sdklog's BatchProcessor defaults to 1s,
// sdktrace's BatchSpanProcessor to 5s) — QueueOptions never overrides a field
// it wasn't given a positive value for.
type QueueOptions struct {
	// MaxQueueSize is the maximum number of records/spans buffered before the
	// non-blocking-drop default (#358) starts refusing new ones. 0 leaves the
	// SDK default (2048 for both signals).
	MaxQueueSize int
	// ExportMaxBatchSize bounds the number of records/spans per export call. 0
	// leaves the SDK default (512 for both signals).
	ExportMaxBatchSize int
	// ExportInterval is the maximum time between batched exports when the
	// queue does not reach ExportMaxBatchSize first. 0 leaves the SDK default:
	// 1s for logs, 5s for traces.
	ExportInterval time.Duration
	// ExportTimeout bounds a single export call. 0 leaves the SDK default
	// (30s for both signals).
	ExportTimeout time.Duration
}

// isZero reports whether every field is unset, meaning the caller wants pure
// SDK-default behavior for this signal.
func (q QueueOptions) isZero() bool {
	return q.MaxQueueSize == 0 && q.ExportMaxBatchSize == 0 && q.ExportInterval == 0 && q.ExportTimeout == 0
}

// resolvedQueueConfig is QueueOptions after applying signal-specific defaults —
// used only by this package's own queueingLogProcessor/queueingSpanProcessor
// (built when a BatchQueueTracker is supplied), since that path cannot rely on
// the SDK's own env-var/default resolution the way sdkLogOptions/sdkSpanOptions
// (passed straight to the real SDK constructors) can.
type resolvedQueueConfig struct {
	maxQueueSize  int
	batchSize     int
	interval      time.Duration
	exportTimeout time.Duration
}

// dfltLogQueueConfig/dfltTraceQueueConfig mirror sdklog.NewBatchProcessor's and
// sdktrace.NewBatchSpanProcessor's own compiled-in defaults (batch.go's
// dfltMaxQSize/dfltExpInterval/dfltExpTimeout/dfltExpMaxBatchSize;
// batch_span_processor.go's DefaultMaxQueueSize/DefaultScheduleDelay/
// DefaultExportTimeout/DefaultMaxExportBatchSize) — confirmed against the
// pinned sdk/log@v0.20.0 and sdk@v1.44.0/trace sources. The one difference
// between signals is the export interval: 1s for logs, 5s for traces.
var (
	dfltLogQueueConfig   = resolvedQueueConfig{maxQueueSize: 2048, batchSize: 512, interval: time.Second, exportTimeout: 30 * time.Second}
	dfltTraceQueueConfig = resolvedQueueConfig{maxQueueSize: 2048, batchSize: 512, interval: 5 * time.Second, exportTimeout: 30 * time.Second}
)

// resolve applies q's positive fields on top of dflt.
func (q QueueOptions) resolve(dflt resolvedQueueConfig) resolvedQueueConfig {
	out := dflt
	if q.MaxQueueSize > 0 {
		out.maxQueueSize = q.MaxQueueSize
	}
	if q.ExportMaxBatchSize > 0 {
		out.batchSize = q.ExportMaxBatchSize
	}
	if q.ExportInterval > 0 {
		out.interval = q.ExportInterval
	}
	if q.ExportTimeout > 0 {
		out.exportTimeout = q.ExportTimeout
	}
	return out
}

// sdkLogOptions returns the sdklog.BatchProcessorOptions equivalent to q, or
// nil when q is zero — nil means newLogProcessor's
// `sdklog.NewBatchProcessor(exp, opts.Batch.Logs.sdkLogOptions()...)` becomes
// exactly `sdklog.NewBatchProcessor(exp)`, the same call this package made
// before #358 existed.
func (q QueueOptions) sdkLogOptions() []sdklog.BatchProcessorOption {
	if q.isZero() {
		return nil
	}
	var opts []sdklog.BatchProcessorOption
	if q.MaxQueueSize > 0 {
		opts = append(opts, sdklog.WithMaxQueueSize(q.MaxQueueSize))
	}
	if q.ExportMaxBatchSize > 0 {
		opts = append(opts, sdklog.WithExportMaxBatchSize(q.ExportMaxBatchSize))
	}
	if q.ExportInterval > 0 {
		opts = append(opts, sdklog.WithExportInterval(q.ExportInterval))
	}
	if q.ExportTimeout > 0 {
		opts = append(opts, sdklog.WithExportTimeout(q.ExportTimeout))
	}
	return opts
}

// sdkSpanOptions is sdkLogOptions' trace-side counterpart, mapping onto
// sdktrace.BatchSpanProcessorOption (ExportInterval -> WithBatchTimeout).
func (q QueueOptions) sdkSpanOptions() []sdktrace.BatchSpanProcessorOption {
	if q.isZero() {
		return nil
	}
	var opts []sdktrace.BatchSpanProcessorOption
	if q.MaxQueueSize > 0 {
		opts = append(opts, sdktrace.WithMaxQueueSize(q.MaxQueueSize))
	}
	if q.ExportMaxBatchSize > 0 {
		opts = append(opts, sdktrace.WithMaxExportBatchSize(q.ExportMaxBatchSize))
	}
	if q.ExportInterval > 0 {
		opts = append(opts, sdktrace.WithBatchTimeout(q.ExportInterval))
	}
	if q.ExportTimeout > 0 {
		opts = append(opts, sdktrace.WithExportTimeout(q.ExportTimeout))
	}
	return opts
}

// BatchOptions tunes the log and span processor queues (#358): capacity,
// export batch size, export interval, and export timeout, per signal — plus
// optional saturation/drop telemetry via Tracker.
//
// A zero value reproduces exactly today's `sdklog.NewBatchProcessor(exp)` /
// `sdktrace.NewBatchSpanProcessor(exp)` calls: Tracker is nil, so
// newLogProcessor/newSpanProcessor never build a wrapping processor at all,
// and Logs/Traces being zero QueueOptions means sdkLogOptions()/
// sdkSpanOptions() append no functional options — so it is the exact same
// zero-argument constructor call this package made before #358 existed.
type BatchOptions struct {
	Logs   QueueOptions
	Traces QueueOptions

	// Tracker, when non-nil, makes queue occupancy/capacity/drop telemetry
	// observable (see BatchQueueTracker) by switching newLogProcessor/
	// newSpanProcessor to this package's own queueingLogProcessor/
	// queueingSpanProcessor instead of the plain SDK constructors — needed
	// because sdklog.BatchProcessor and sdktrace's batch span processor are
	// both unexported types with no queue-size or dropped-count accessor
	// (confirmed against the pinned sdk/log@v0.20.0 and sdk@v1.44.0/trace
	// sources; see the #358 report). Nil (the zero value) keeps behavior
	// identical to before this option existed, with zero added overhead.
	//
	// Scope a Tracker to ONE Provider, matching CardinalityTracker (also a
	// Provider-scoped field, never process-wide): sharing one Tracker across
	// multiple NewProvider calls (e.g. one per tailnet in a ProviderSet) would
	// have each provider's registration overwrite the last under the same
	// "logs"/"traces" key, losing every earlier provider's queue visibility.
	Tracker *BatchQueueTracker
}

// StdoutOptions makes the "stdout" protocol an immediate debugging sink
// (#384) rather than one on production batching/interval schedules. There is
// no separate "Immediate" toggle: whenever Options.Protocol == "stdout",
// newLogProcessor/newSpanProcessor always use the SDK's Simple processors and
// newMetricReader always shortens the export interval. Stdout is documented as
// a local debugging sink with no production reliability promise (no retry, no
// file rotation), so there is no reason to also offer a "batched stdout" mode
// that would need an explicit opt-in — this package makes "immediate" the
// unconditional effective default for the stdout protocol specifically. A
// zero StdoutOptions is exactly this behavior; the only thing it tunes is HOW
// short the metric interval is (and, once wired — see the #384 report's
// WIRING REQUEST — how the stdout exporters frame their output), never
// whether records/spans are immediate.
type StdoutOptions struct {
	// MetricInterval overrides the metric PeriodicReader interval used when
	// Protocol == "stdout" and Options.MetricInterval is left unset (<=0). An
	// explicit Options.MetricInterval always wins regardless of protocol. Zero
	// uses stdoutDefaultMetricInterval (5s).
	MetricInterval time.Duration

	// Pretty selects the stdout exporters' indented multi-line JSON framing
	// (stdoutlog/stdoutmetric/stdouttrace's WithPrettyPrint) instead of the
	// default compact one-line-per-record JSON.
	//
	// NOT YET WIRED: the stdout exporters are constructed in exporters.go,
	// which this field's owning lane (#358/#384) does not touch under its file
	// ownership — see the report's WIRING REQUEST for the three-line change
	// needed there to apply it. The field is defined here (not in
	// exporters.go) because it is a stdout-specific debugging concern that
	// belongs with the rest of StdoutOptions.
	Pretty bool
}

// batchQueue is a small, from-scratch bounded batching queue behind
// queueingLogProcessor/queueingSpanProcessor, built to make queue occupancy
// and drop counts OBSERVABLE (#358) — a property neither sdklog.BatchProcessor
// nor sdktrace's batch span processor expose: both are unexported concrete
// types (go.opentelemetry.io/otel/sdk/log@v0.20.0's batch.go;
// go.opentelemetry.io/otel/sdk@v1.44.0/trace's batch_span_processor.go) with no
// queue-size or dropped-count accessor. Confirmed by reading both pinned
// sources — see the #358 report's OBSERVABILITY REALITY CHECK.
//
// Semantics deliberately mirror what this app already relies on from the SDK
// defaults: enqueue never blocks the caller (OnEmit/OnEnd) — a full queue
// drops the newest item and counts it, the explicit non-blocking default #358
// asks for — and a background goroutine flushes a batch to exportFn on
// batch-size OR interval, whichever comes first, each export bounded by
// exportTimeout. Unlike sdklog's own ring buffer (which evicts the OLDEST
// entry to keep the newest), this queue refuses the NEWEST entry when full,
// matching sdktrace's batch span processor's own non-blocking-drop policy —
// see the #358 report for why the two signals' upstream defaults actually
// differ here and this queue picks one behavior for both.
type batchQueue[T any] struct {
	items   chan T
	dropped atomic.Uint64

	batchSize     int
	exportTimeout time.Duration
	exportFn      func(context.Context, []T) error

	trigger chan struct{}
	kill    chan struct{}
	done    chan struct{}
	stopped atomic.Bool
}

func newBatchQueue[T any](capacity, batchSize int, interval, exportTimeout time.Duration, exportFn func(context.Context, []T) error) *batchQueue[T] {
	if capacity < 1 {
		capacity = 1
	}
	if batchSize < 1 {
		batchSize = 1
	}
	if interval <= 0 {
		interval = time.Second
	}
	q := &batchQueue[T]{
		items:         make(chan T, capacity),
		batchSize:     batchSize,
		exportTimeout: exportTimeout,
		exportFn:      exportFn,
		trigger:       make(chan struct{}, 1),
		kill:          make(chan struct{}),
	}
	q.done = q.run(interval)
	return q
}

// enqueue offers v without blocking the caller. When the queue is full, v is
// dropped (refused) and the drop counter increments.
func (q *batchQueue[T]) enqueue(v T) {
	if q.stopped.Load() {
		return
	}
	select {
	case q.items <- v:
		if len(q.items) >= q.batchSize {
			select {
			case q.trigger <- struct{}{}:
			default:
			}
		}
	default:
		q.dropped.Add(1)
	}
}

func (q *batchQueue[T]) size() int     { return len(q.items) }
func (q *batchQueue[T]) capacity() int { return cap(q.items) }

// takeDropped returns the drop count accumulated since the last call and
// resets it to zero — the same swap-and-reset shape as
// CardinalityTracker.Report, so BatchQueueTracker.Report emits a per-interval
// Counter delta rather than re-reporting the lifetime total on every call.
func (q *batchQueue[T]) takeDropped() uint64 { return q.dropped.Swap(0) }

// run spawns the background flush goroutine and returns its done channel.
func (q *batchQueue[T]) run(interval time.Duration) chan struct{} {
	done := make(chan struct{})
	ticker := time.NewTicker(interval)
	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				q.flush()
			case <-q.trigger:
				q.flush()
			case <-q.kill:
				return
			}
		}
	}()
	return done
}

// drainBatch pulls up to batchSize items currently available without
// blocking.
func (q *batchQueue[T]) drainBatch() []T {
	buf := make([]T, 0, q.batchSize)
	for len(buf) < q.batchSize {
		select {
		case v := <-q.items:
			buf = append(buf, v)
		default:
			return buf
		}
	}
	return buf
}

// exportBatch hands buf to exportFn under a context bounded by exportTimeout
// (when set). exportFn runs in the caller's goroutine, so a stalled exporter
// stalls only the caller (the background flush loop, or a drainAll during
// Shutdown/ForceFlush) — never a concurrent OnEmit/OnEnd, which is exactly
// what keeps enqueue non-blocking under a stuck backend.
func (q *batchQueue[T]) exportBatch(ctx context.Context, buf []T) error {
	if q.exportTimeout <= 0 {
		return q.exportFn(ctx, buf)
	}
	cctx, cancel := context.WithTimeout(ctx, q.exportTimeout)
	defer cancel()
	return q.exportFn(cctx, buf)
}

// flush drains and exports a single batch (best-effort; matches the SDK's own
// no-retry contract for both signals). Called from the background loop only.
func (q *batchQueue[T]) flush() {
	buf := q.drainBatch()
	if len(buf) == 0 {
		return
	}
	_ = q.exportBatch(context.Background(), buf)
}

// drainAll repeatedly drains and exports whatever is queued until empty or an
// export fails, used by both shutdown and forceFlush.
func (q *batchQueue[T]) drainAll(ctx context.Context) error {
	for {
		buf := q.drainBatch()
		if len(buf) == 0 {
			return nil
		}
		if err := q.exportBatch(ctx, buf); err != nil {
			return err
		}
		if len(buf) < q.batchSize {
			return nil
		}
	}
}

// shutdown stops the background goroutine and makes a final best-effort drain
// of anything left queued. Idempotent.
func (q *batchQueue[T]) shutdown(ctx context.Context) error {
	if q.stopped.Swap(true) {
		return nil
	}
	close(q.kill)
	select {
	case <-q.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return q.drainAll(ctx)
}

// forceFlush drains and exports everything currently queued without stopping
// the background loop.
func (q *batchQueue[T]) forceFlush(ctx context.Context) error {
	return q.drainAll(ctx)
}

// queueGauge is the read-only surface a *batchQueue[T] exposes to
// BatchQueueTracker, for any item type T — no T appears in the interface
// itself, so *batchQueue[T] satisfies it for both sdklog.Record and
// sdktrace.ReadOnlySpan without any adapter.
type queueGauge interface {
	size() int
	capacity() int
	takeDropped() uint64
}

// attrDropReason/dropReasonQueueFull are the "reason" attribute and its one
// value for tailscale2otel.processor.dropped, per the #382 naming decision.
// Not in internal/semconv because this lane does not own that file; see the
// report's CATALOG REGISTRATION section for the constant a follow-up could add
// there.
const (
	attrDropReason      = "reason"
	dropReasonQueueFull = "queue_full"
)

// docQueueSize/docQueueCapacity/docQueueDropped declare the three #358 metrics
// (names frozen by #382's decision record) for the doc generator. They are
// NOT registered in catalog.go's Catalog() — this lane owns only
// processors.go; see the report's CATALOG REGISTRATION section for the exact
// lines to add there.
var (
	docQueueSize = metricdoc.Metric{
		Name:        "tailscale2otel.processor.queue.size",
		Unit:        "1",
		Instrument:  metricdoc.Gauge,
		Description: "Current number of records/spans buffered in this app's own log/trace batch processor queue, awaiting export, by signal.",
		Attributes:  []string{semconv.AttrExportSignal},
		Group:       groupSelfObs,
	}
	docQueueCapacity = metricdoc.Metric{
		Name:        "tailscale2otel.processor.queue.capacity",
		Unit:        "1",
		Instrument:  metricdoc.Gauge,
		Description: "Configured maximum size of this app's own log/trace batch processor queue, by signal.",
		Attributes:  []string{semconv.AttrExportSignal},
		Group:       groupSelfObs,
	}
	docQueueDropped = metricdoc.Metric{
		Name:        "tailscale2otel.processor.dropped",
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "Records/spans dropped because this app's own log/trace batch processor queue was full when offered, by signal and reason.",
		Attributes:  []string{semconv.AttrExportSignal, attrDropReason},
		Group:       groupSelfObs,
	}
)

// BatchQueueTracker exposes the log/trace processor queues' current
// occupancy, capacity, and drop counts as telemetry (#358, names frozen by
// #382's decision record): tailscale2otel.processor.queue.size{signal},
// .queue.capacity{signal}, and .dropped{signal,reason="queue_full"}.
//
// The caller owns constructing one (NewBatchQueueTracker), assigning it to
// Options.Batch.Tracker BEFORE calling NewProvider — newLogProcessor/
// newSpanProcessor register their queue with it during construction — and
// then periodically calling Report with a real Emitter. This package does not
// schedule that itself, matching how internal/app/cardinality.go drives
// CardinalityTracker.Report on the export interval; the same pattern applies
// here (see the report's WIRING REQUEST for the concrete accessor a follow-up
// would need on Provider to reach the Tracker from the app layer).
//
// A nil Tracker (the zero BatchOptions default) is a no-op on Report, exactly
// like CardinalityTracker.
type BatchQueueTracker struct {
	mu     sync.Mutex
	queues map[string]queueGauge
}

// NewBatchQueueTracker returns an empty tracker ready to be assigned to
// Options.Batch.Tracker before calling NewProvider.
func NewBatchQueueTracker() *BatchQueueTracker {
	return &BatchQueueTracker{queues: make(map[string]queueGauge, 2)}
}

// register records q as the queue for signal ("logs" | "traces"). Called by
// newLogProcessor/newSpanProcessor when they build a queueing processor.
func (t *BatchQueueTracker) register(signal string, q queueGauge) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.queues[signal] = q
	t.mu.Unlock()
}

// Report emits the current queue.size/queue.capacity gauges and the
// dropped-since-last-Report counter delta for every registered signal, in
// stable (sorted by signal) order. No-op on a nil Tracker.
func (t *BatchQueueTracker) Report(e Emitter) {
	if t == nil {
		return
	}
	t.mu.Lock()
	signals := make([]string, 0, len(t.queues))
	for signal := range t.queues {
		signals = append(signals, signal)
	}
	sort.Strings(signals)
	queues := make([]queueGauge, len(signals))
	for i, signal := range signals {
		queues[i] = t.queues[signal]
	}
	t.mu.Unlock()

	for i, signal := range signals {
		q := queues[i]
		attrs := Attrs{semconv.AttrExportSignal: signal}
		e.Gauge(docQueueSize.Name, docQueueSize.Unit, docQueueSize.Description, float64(q.size()), attrs)
		e.Gauge(docQueueCapacity.Name, docQueueCapacity.Unit, docQueueCapacity.Description, float64(q.capacity()), attrs)
		e.Counter(docQueueDropped.Name, docQueueDropped.Unit, docQueueDropped.Description, float64(q.takeDropped()),
			Attrs{semconv.AttrExportSignal: signal, attrDropReason: dropReasonQueueFull})
	}
}

// queueingLogProcessor is the log-signal instantiation of batchQueue, used
// only when a BatchQueueTracker is supplied (see newLogProcessor).
type queueingLogProcessor struct {
	q *batchQueue[sdklog.Record]
}

func newQueueingLogProcessor(exp sdklog.Exporter, cfg resolvedQueueConfig) *queueingLogProcessor {
	p := &queueingLogProcessor{}
	p.q = newBatchQueue(cfg.maxQueueSize, cfg.batchSize, cfg.interval, cfg.exportTimeout,
		func(ctx context.Context, batch []sdklog.Record) error {
			return exp.Export(ctx, batch)
		})
	return p
}

// Enabled reports whether this processor will process for the given context
// and param; always true, matching sdklog.BatchProcessor.
func (p *queueingLogProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

// OnEmit enqueues a clone of r (the Processor contract requires cloning
// before retaining a Record beyond the call — see sdklog.Record.Clone's doc).
func (p *queueingLogProcessor) OnEmit(_ context.Context, r *sdklog.Record) error {
	p.q.enqueue(r.Clone())
	return nil
}

func (p *queueingLogProcessor) Shutdown(ctx context.Context) error   { return p.q.shutdown(ctx) }
func (p *queueingLogProcessor) ForceFlush(ctx context.Context) error { return p.q.forceFlush(ctx) }

var _ sdklog.Processor = (*queueingLogProcessor)(nil)

// queueingSpanProcessor is the trace-signal instantiation of batchQueue, used
// only when a BatchQueueTracker is supplied (see newSpanProcessor).
type queueingSpanProcessor struct {
	q *batchQueue[sdktrace.ReadOnlySpan]
}

func newQueueingSpanProcessor(exp sdktrace.SpanExporter, cfg resolvedQueueConfig) *queueingSpanProcessor {
	p := &queueingSpanProcessor{}
	p.q = newBatchQueue(cfg.maxQueueSize, cfg.batchSize, cfg.interval, cfg.exportTimeout,
		func(ctx context.Context, batch []sdktrace.ReadOnlySpan) error {
			return exp.ExportSpans(ctx, batch)
		})
	return p
}

func (*queueingSpanProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (p *queueingSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan)                 { p.q.enqueue(s) }
func (p *queueingSpanProcessor) Shutdown(ctx context.Context) error            { return p.q.shutdown(ctx) }
func (p *queueingSpanProcessor) ForceFlush(ctx context.Context) error          { return p.q.forceFlush(ctx) }

var _ sdktrace.SpanProcessor = (*queueingSpanProcessor)(nil)
