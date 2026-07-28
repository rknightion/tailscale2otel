package telemetry

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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

// newMetricReader builds the OTLP metric reader for opts. It is the single seam
// NewProvider uses, so reader policy (interval, export batching, per-protocol
// immediacy, export timeout) is decided here rather than at the call site.
func newMetricReader(exp sdkmetric.Exporter, opts Options) (*sdkmetric.PeriodicReader, error) {
	interval := opts.MetricInterval
	if interval <= 0 {
		interval = defaultMetricInterval
	}
	return newPeriodicMetricReader(exp, interval, opts.MetricExportBatchSize)
}

// newLogProcessor builds the log processor for opts. It is the single seam
// NewProvider uses, so queue/batch/immediacy policy is decided here.
func newLogProcessor(exp sdklog.Exporter, _ Options) sdklog.Processor {
	return sdklog.NewBatchProcessor(exp)
}

// newSpanProcessor builds the span processor for opts. It is the single seam
// NewProvider uses, so queue/batch/immediacy policy is decided here.
func newSpanProcessor(exp sdktrace.SpanExporter, _ Options) sdktrace.SpanProcessor {
	return sdktrace.NewBatchSpanProcessor(exp)
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

// BatchOptions tunes the log and span processor queues (#358). Owned by the
// #358/#384 lane. A zero value must reproduce the SDK defaults exactly.
type BatchOptions struct{}

// StdoutOptions makes the "stdout" protocol immediate rather than batched
// (#384). Owned by the #358/#384 lane. A zero value keeps current behavior.
type StdoutOptions struct{}
