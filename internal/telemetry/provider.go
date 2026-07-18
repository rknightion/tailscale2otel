package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/credentials"

	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
)

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

// scopeName is the instrumentation scope for all emitted telemetry.
const scopeName = "github.com/rknightion/tailscale2otel"

// Options configures the OTLP/stdout telemetry pipeline.
type Options struct {
	ServiceName    string
	ServiceVersion string
	InstanceID     string

	Protocol string // "grpc" | "http" | "stdout" (empty defaults to "http")
	Endpoint string // full URL for http (incl. /otlp); host:port for grpc
	Headers  map[string]string

	Insecure bool // plaintext: disable TLS entirely (h2c / http://) — NOT a cert-verify skip
	// InsecureSkipVerify keeps TLS on but skips server-certificate verification
	// (self-signed / private-CA gateways for testing). Distinct from Insecure,
	// which sends everything in plaintext. Default false (#94).
	InsecureSkipVerify bool
	CAFile             string
	CertFile           string
	KeyFile            string

	MetricInterval time.Duration // PeriodicReader interval (default 60s)

	// CardinalityLimit is the hard per-instrument limit on the number of distinct
	// attribute sets collected per cycle; sets beyond it collapse into the SDK's
	// otel_metric_overflow series. 0 or negative means unlimited. The app layer
	// supplies the configured default (10000); the same value caps the
	// self-observability series tracker so series.active pins exactly at the limit.
	CardinalityLimit int
	// CardinalityLabelValueCap bounds how many distinct VALUES per (metric,label)
	// the self-observability tracker retains for the status page's label-cardinality
	// views. 0 disables label-value capture. The app supplies the configured value.
	CardinalityLabelValueCap int

	// SelfObsEnabled turns on self-observability instrumentation, including the
	// tailscale2otel.series.active cardinality tracker (nil/disabled otherwise).
	SelfObsEnabled bool

	// TracingEnabled turns on the OTEL TracerProvider (and flips the metric
	// exemplar filter to trace-based). When false, Tracer() returns a no-op
	// tracer and exemplars stay disabled (zero reservoir cost).
	TracingEnabled bool

	// TraceSampler selects the head sampler when tracing is enabled. One of
	// always_on, always_off, traceidratio, parentbased_always_on,
	// parentbased_traceidratio (validated by the config layer). Empty defaults to
	// parentbased_always_on.
	TraceSampler string

	// TraceSamplerArg is the ratio in [0,1] for the *traceidratio samplers;
	// ignored by the others.
	TraceSamplerArg float64

	// PrometheusEnabled attaches an additional Prometheus metric.Reader (a
	// per-Provider registry) alongside the OTLP reader, so the same instruments are
	// scrapeable at /metrics. The HTTP serving lives in the app layer.
	PrometheusEnabled bool

	// Provider is the control-plane backend (tailscale|headscale); emitted as the
	// tailscale2otel.provider resource attribute when non-empty.
	Provider string

	// TailnetName, when non-empty, is emitted as the tailscale.tailnet resource
	// attribute. The process-level provider leaves it empty; each per-tailnet
	// provider sets it so all of that tailnet's signals carry the dimension in the
	// Resource. Pair it with a distinct InstanceID per tailnet (see ProviderSet)
	// so each tailnet is its own OTLP target and series don't collide.
	TailnetName string

	// PIIFilter controls which PII / identifier categories are emitted. A nil map
	// (or a map where every category is true) is a no-op fast path. Set any
	// category to false to drop those identifiers at the emit site.
	PIIFilter pii.Categories

	// StdoutWriter overrides the destination in "stdout" protocol (default os.Stdout).
	StdoutWriter io.Writer

	// Logger receives diagnostics from the telemetry pipeline (currently
	// label-collision resolutions in the Emitter). Nil disables that logging.
	Logger *slog.Logger
}

// Provider owns the OTEL MeterProvider and LoggerProvider and exposes a single
// Emitter for collectors. Shutdown flushes and releases both.
type Provider struct {
	mp      *sdkmetric.MeterProvider
	lp      *sdklog.LoggerProvider
	tp      *sdktrace.TracerProvider // nil unless TracingEnabled
	tracer  trace.Tracer             // always non-nil (no-op when tp is nil)
	emitter Emitter
	card    *CardinalityTracker // nil unless self-observability is enabled

	metricCounter *countingMetricExporter // nil unless self-obs enabled
	logCounter    *countingLogExporter    // nil unless self-obs enabled

	promReg *prometheus.Registry // nil unless Options.PrometheusEnabled
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

// NewProvider builds the telemetry pipeline for the given options.
func NewProvider(ctx context.Context, opts Options) (*Provider, error) {
	// stdout protocol only: the metric/log/trace exporters share one writer and
	// flush concurrently, so serialize writes to it. opts is a value copy, so this
	// rewrite is local to this Provider and never mutates the caller's Options.
	if opts.Protocol == "stdout" {
		w := opts.StdoutWriter
		if w == nil {
			w = os.Stdout
		}
		opts.StdoutWriter = &lockedWriter{w: w}
	}
	// Two resources by design: metrics OMIT service.version (see buildResource —
	// it would become a per-series service_version label on Grafana Cloud and churn
	// the whole series set every redeploy, #187), logs/traces KEEP it. Everything
	// else (service.name, service.instance.id, host/os/process detectors) is
	// identical.
	metricRes, err := buildResource(ctx, opts, false)
	if err != nil {
		return nil, fmt.Errorf("build metrics resource: %w", err)
	}
	logRes, err := buildResource(ctx, opts, true)
	if err != nil {
		return nil, fmt.Errorf("build logs resource: %w", err)
	}
	constAttrs := constLabelAttrs(opts)
	metricExp, err := newMetricExporter(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("metric exporter: %w", err)
	}
	logExp, err := newLogExporter(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("log exporter: %w", err)
	}

	var metricCounter *countingMetricExporter
	var logCounter *countingLogExporter
	if opts.SelfObsEnabled {
		metricCounter = newCountingMetricExporter(metricExp)
		metricExp = metricCounter
		logCounter = newCountingLogExporter(logExp)
		logExp = logCounter
	}

	interval := opts.MetricInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	mpOpts := append(
		metricProviderOptions(metricRes, opts.CardinalityLimit, opts.TracingEnabled),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(interval))),
	)
	var promReg *prometheus.Registry
	if opts.PrometheusEnabled {
		promReg = prometheus.NewRegistry()
		promReader, err := newPrometheusReader(promReg)
		if err != nil {
			return nil, fmt.Errorf("prometheus reader: %w", err)
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(promReader))
	}
	mp := sdkmetric.NewMeterProvider(mpOpts...)
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(logRes),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
	)

	var tp *sdktrace.TracerProvider
	if opts.TracingEnabled {
		traceExp, err := newTraceExporter(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("trace exporter: %w", err)
		}
		tpOpts := []sdktrace.TracerProviderOption{
			sdktrace.WithResource(logRes),
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithSampler(buildSampler(opts.TraceSampler, opts.TraceSamplerArg)),
		}
		if len(constAttrs) > 0 {
			tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(constAttrSpanProcessor{attrs: constAttrs}))
		}
		tp = sdktrace.NewTracerProvider(tpOpts...)
	}
	tracer := tracenoop.NewTracerProvider().Tracer(scopeName)
	if tp != nil {
		tracer = tp.Tracer(scopeName)
	}

	var card *CardinalityTracker
	if opts.SelfObsEnabled {
		card = NewCardinalityTrackerWithLimits(opts.CardinalityLimit, opts.CardinalityLabelValueCap)
	}

	emitter := newOtelEmitter(mp.Meter(scopeName), lp.Logger(scopeName), card, reservedPromotedLabels(opts), opts.Logger, opts.PIIFilter, constAttrs)

	if opts.SelfObsEnabled {
		// Late-bind the duration observer now that the Emitter exists (the
		// decorators were constructed before it). Each decorator already knows its
		// own signal; EmitExportDuration records the histogram on the next cycle.
		// Caveat: the observation a given Export() produces is exported on the
		// following cycle, so the final flush during Shutdown is observed but its
		// data point typically never ships (acceptable — one lost point at exit).
		obs := func(signal, outcome string, seconds float64) {
			EmitExportDuration(emitter, signal, outcome, seconds)
		}
		metricCounter.setObserver(obs)
		logCounter.setObserver(obs)
	}

	return &Provider{
		mp:      mp,
		lp:      lp,
		tp:      tp,
		tracer:  tracer,
		emitter: emitter,
		card:    card,

		metricCounter: metricCounter,
		logCounter:    logCounter,

		promReg: promReg,
	}, nil
}

// ExportStats returns the cumulative count of data points and log records handed
// to the OTLP exporters since start. Zero when self-observability is disabled
// (no counting wrappers were installed). Safe to call concurrently.
func (p *Provider) ExportStats() ExportStats {
	var s ExportStats
	if p.metricCounter != nil {
		s.Datapoints = p.metricCounter.count()
	}
	if p.logCounter != nil {
		s.LogRecords = p.logCounter.count()
	}
	return s
}

// Emitter returns the Emitter collectors should use.
func (p *Provider) Emitter() Emitter { return p.emitter }

// PromGatherer returns this provider's Prometheus registry as a Gatherer, or nil
// when the Prometheus reader is disabled. Each provider owns its own registry so a
// single shared registry never sees inconsistent target_info label dimensions.
func (p *Provider) PromGatherer() prometheus.Gatherer {
	if p.promReg == nil {
		return nil
	}
	return p.promReg
}

// Cardinality returns the self-observability cardinality tracker, or nil when
// self-observability is disabled. The caller drives Report on the export
// interval and may call Report safely even when this is nil.
func (p *Provider) Cardinality() *CardinalityTracker { return p.card }

// Tracer returns the tracer collectors-adjacent infrastructure (scheduler,
// tsapi transport, receivers) records spans with. When tracing is disabled it is
// a no-op tracer, so callers never need to nil-check.
func (p *Provider) Tracer() trace.Tracer { return p.tracer }

// Shutdown flushes and stops the metric, log, and trace pipelines. The three are
// independent exporters, so they are shut down concurrently under ctx (see
// shutdownAll / #204): a metric exporter blocked on an unresponsive backend must
// not consume the shared shutdown budget and rob the log and trace pipelines of
// their chance to flush.
func (p *Provider) Shutdown(ctx context.Context) error {
	fns := []func(context.Context) error{p.mp.Shutdown, p.lp.Shutdown}
	if p.tp != nil {
		fns = append(fns, p.tp.Shutdown)
	}
	return shutdownAll(ctx, fns...)
}

// shutdownAll runs every shutdown function concurrently under ctx and returns the
// joined error of all of them.
//
// Concurrency is the fix for #204: running the independent telemetry pipelines
// (and independent tailnet providers) sequentially lets a single blocked exporter
// consume the whole shared shutdown budget, so every later pipeline/provider is
// handed an already-expired context and drops its buffered signals even when its
// own backend is healthy. Running them concurrently gives each an independent shot
// at flushing within the single overall deadline: a blocked exporter occupies only
// its own goroutine, never another pipeline's opportunity to flush. Each function
// gets its own child context (canceled as soon as it returns) so a completed
// pipeline releases its context resources promptly; the shared deadline on ctx
// remains the overall ceiling. A genuinely blocked function still returns via that
// deadline (ctx.Err()), and its error is retained in the join — no goroutine leak.
func shutdownAll(ctx context.Context, fns ...func(context.Context) error) error {
	if len(fns) == 0 {
		return nil
	}
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	wg.Add(len(fns))
	for i, fn := range fns {
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithCancel(ctx)
			defer cancel()
			errs[i] = fn(cctx)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// buildResource builds the OTEL resource. includeServiceVersion controls whether
// service.version is attached: it is TRUE for the logs/traces resource and FALSE
// for the metrics resource.
//
// The split exists because the OTLP->Prometheus convention promotes only
// service.name (+service.namespace)->job and service.instance.id->instance to
// labels; every other resource attribute belongs on the target_info info metric.
// Grafana Cloud's OTLP ingest deviates and promotes the whole service.* namespace,
// so a service.version on the metrics resource becomes a service_version label on
// EVERY series. That makes each build mint a fresh series set: after a redeploy the
// old and new versions' series coexist for the query lookback window (an OTLP push
// carries no staleness signal, unlike a scrape target going down), so any panel
// that sums across a bounded dimension transiently doubles — and active-series
// cardinality grows by the number of versions ever seen (#187; the doubling was
// diagnosed live in graph2otel#104, which runs a per-commit :main build).
//
// Version stays queryable from metrics via the tailscale2otel.build_info gauge
// (join with group_left). Logs and traces are never summed and have no per-series
// label surface, so their resource keeps service.version for per-record/-span
// version attribution.
func buildResource(ctx context.Context, opts Options, includeServiceVersion bool) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{attribute.String("service.name", opts.ServiceName)}
	if includeServiceVersion && opts.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", opts.ServiceVersion))
	}
	if opts.InstanceID != "" {
		attrs = append(attrs, attribute.String("service.instance.id", opts.InstanceID))
	}
	// The schemaless WithAttributes block carries the service.* identity; the core
	// detectors add host/os/process attributes so multiple instances are
	// distinguishable in Grafana. All detectors share one semconv schema URL, so
	// merging them with the schemaless block cannot raise a schema-URL conflict.
	// A narrow process subset is used deliberately — WithProcess() would also
	// emit process.command_args and process.owner, which can leak deploy paths
	// and usernames to the backend.
	detectors := []resource.Option{
		resource.WithAttributes(attrs...),
		resource.WithTelemetrySDK(),
		resource.WithOS(),
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
	}
	// When the Hostnames category is explicitly disabled, omit WithHost() so that
	// host.name is never included in the Resource (it would otherwise be promoted
	// to target_info and leak the hostname to the backend).
	hostnamesOff := false
	if v, ok := opts.PIIFilter[pii.CatHostnames]; ok && !v {
		hostnamesOff = true
	}
	if !hostnamesOff {
		detectors = append(detectors, resource.WithHost())
	}
	res, err := resource.New(ctx, detectors...)
	// A partial resource (a detector that couldn't read its source — e.g.
	// os.Hostname() failing) must NOT abort startup: the exporter's core job is
	// unaffected, so continue with whatever attributes were resolved. Any other
	// error (which, given the shared schema URL, should not occur) is fatal.
	if err != nil && errors.Is(err, resource.ErrPartialResource) {
		return res, nil
	}
	return res, err
}

// constLabelAttrs returns the provider-scoped attributes stamped onto every signal
// (metric data point, log record, span) for a provider built from opts: the
// tailnet name and control-plane provider, each included only when non-empty.
// Roadmap item L moved these off the Resource so they are real, joinless labels on
// every backend (Grafana Cloud, the Prometheus pull endpoint, self-managed Mimir).
//
// PII gate: when opts.PIIFilter explicitly disables pii.CatTailnetName (i.e. the
// category is present in the map and set to false), the tailscale.tailnet attribute
// is omitted. In multi-tailnet mode this removes the per-tailnet label from all
// signals for that provider; per-tailnet series still remain distinct via the
// service.instance.id resource attribute. Category absent from the map, or present
// and true, behaves as today (attribute emitted). The tailscale2otel.provider
// attribute is NOT PII and is always included when non-empty.
func constLabelAttrs(opts Options) []attribute.KeyValue {
	var out []attribute.KeyValue
	if opts.TailnetName != "" {
		// Omit tailscale.tailnet when the operator has explicitly disabled the
		// tailnet_name PII category (same gate pattern as buildResource/hostnames).
		if v, ok := opts.PIIFilter[pii.CatTailnetName]; !ok || v {
			out = append(out, attribute.String(semconv.AttrTailnet, opts.TailnetName))
		}
	}
	if opts.Provider != "" {
		out = append(out, attribute.String(semconv.AttrProvider, opts.Provider))
	}
	return out
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

// reservedPromotedLabels returns the Prometheus label names that Grafana Cloud
// promotes from the OTEL *metrics* Resource onto every exported series:
// service.name→job, service.instance.id→instance, plus the service_* labels
// (confirmed on live series). A data-point attribute that normalizes to one of
// these would duplicate the promoted label and get the whole sample rejected as
// otlp_parse_error, so the Emitter drops it (the resource value wins). Host/OS/
// process resource attributes are deliberately NOT reserved — Grafana keeps those
// in target_info only, so a data-point host.name (e.g. the node-metrics
// passthrough) does not collide.
//
// service_version is deliberately NOT reserved: the metrics resource no longer
// carries service.version (#187), so there is nothing for Grafana Cloud to promote
// and nothing to collide with. It stays on the logs/traces resource, which has no
// per-series label surface and so never reaches this guard.
func reservedPromotedLabels(opts Options) map[string]struct{} {
	r := map[string]struct{}{
		"job":      {},
		"instance": {},
	}
	if opts.ServiceName != "" {
		r["service_name"] = struct{}{}
	}
	if opts.InstanceID != "" {
		r["service_instance_id"] = struct{}{}
	}
	return r
}

// otlpHTTPURL appends the OTLP/HTTP per-signal path (/v1/metrics, /v1/logs) to a
// base endpoint. The OTEL Go otlphttp exporter's WithEndpointURL uses the URL
// path as-is, so a base gateway endpoint (e.g. Grafana Cloud's ".../otlp") must
// have the signal path appended or the gateway returns 404. A base that already
// ends with the signal path is returned unchanged (no double-append).
func otlpHTTPURL(base, signal string) string {
	base = strings.TrimRight(base, "/")
	suffix := "/v1/" + signal
	if strings.HasSuffix(base, suffix) {
		return base
	}
	return base + suffix
}

// cumulativeTemporalitySelector forces cumulative temporality for every
// instrument kind. Grafana Cloud / Mimir OTLP ingestion accepts cumulative only
// (delta is rejected with HTTP 400 and there is no server-side delta->cumulative
// conversion), so we pin it explicitly rather than relying on the SDK default.
func cumulativeTemporalitySelector(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func newMetricExporter(ctx context.Context, opts Options) (sdkmetric.Exporter, error) {
	switch opts.Protocol {
	case "stdout":
		w := opts.StdoutWriter
		if w == nil {
			w = os.Stdout
		}
		return stdoutmetric.New(stdoutmetric.WithWriter(w))
	case "", "http":
		o := []otlpmetrichttp.Option{otlpmetrichttp.WithTemporalitySelector(cumulativeTemporalitySelector)}
		if opts.Endpoint != "" {
			o = append(o, otlpmetrichttp.WithEndpointURL(otlpHTTPURL(opts.Endpoint, "metrics")))
		}
		if len(opts.Headers) > 0 {
			o = append(o, otlpmetrichttp.WithHeaders(opts.Headers))
		}
		if opts.Insecure {
			o = append(o, otlpmetrichttp.WithInsecure())
		} else if tc, err := tlsConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlpmetrichttp.WithTLSClientConfig(tc))
		}
		return otlpmetrichttp.New(ctx, o...)
	case "grpc":
		o := []otlpmetricgrpc.Option{otlpmetricgrpc.WithTemporalitySelector(cumulativeTemporalitySelector)}
		if opts.Endpoint != "" {
			o = append(o, otlpmetricgrpc.WithEndpoint(opts.Endpoint))
		}
		if len(opts.Headers) > 0 {
			o = append(o, otlpmetricgrpc.WithHeaders(opts.Headers))
		}
		if opts.Insecure {
			o = append(o, otlpmetricgrpc.WithInsecure())
		} else if tc, err := tlsConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tc)))
		}
		return otlpmetricgrpc.New(ctx, o...)
	default:
		return nil, fmt.Errorf("unknown otlp protocol %q (want grpc, http, or stdout)", opts.Protocol)
	}
}

func newLogExporter(ctx context.Context, opts Options) (sdklog.Exporter, error) {
	switch opts.Protocol {
	case "stdout":
		w := opts.StdoutWriter
		if w == nil {
			w = os.Stdout
		}
		return stdoutlog.New(stdoutlog.WithWriter(w))
	case "", "http":
		o := []otlploghttp.Option{}
		if opts.Endpoint != "" {
			o = append(o, otlploghttp.WithEndpointURL(otlpHTTPURL(opts.Endpoint, "logs")))
		}
		if len(opts.Headers) > 0 {
			o = append(o, otlploghttp.WithHeaders(opts.Headers))
		}
		if opts.Insecure {
			o = append(o, otlploghttp.WithInsecure())
		} else if tc, err := tlsConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlploghttp.WithTLSClientConfig(tc))
		}
		return otlploghttp.New(ctx, o...)
	case "grpc":
		o := []otlploggrpc.Option{}
		if opts.Endpoint != "" {
			o = append(o, otlploggrpc.WithEndpoint(opts.Endpoint))
		}
		if len(opts.Headers) > 0 {
			o = append(o, otlploggrpc.WithHeaders(opts.Headers))
		}
		if opts.Insecure {
			o = append(o, otlploggrpc.WithInsecure())
		} else if tc, err := tlsConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlploggrpc.WithTLSCredentials(credentials.NewTLS(tc)))
		}
		return otlploggrpc.New(ctx, o...)
	default:
		return nil, fmt.Errorf("unknown otlp protocol %q (want grpc, http, or stdout)", opts.Protocol)
	}
}

// tlsConfig builds a *tls.Config from optional CA/cert/key files, or nil when
// none are configured (use system defaults).
func tlsConfig(opts Options) (*tls.Config, error) {
	if opts.CAFile == "" && opts.CertFile == "" && opts.KeyFile == "" && !opts.InsecureSkipVerify {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: opts.InsecureSkipVerify} //nolint:gosec // G402: opt-in skip-verify knob (otlp.tls.insecure_skip_verify), default false
	if opts.CAFile != "" {
		pem, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in CA file %s", opts.CAFile)
		}
		cfg.RootCAs = pool
	}
	if opts.CertFile != "" && opts.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
