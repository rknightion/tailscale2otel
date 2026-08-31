package telemetry

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetry/pii"
)

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
	// MetricTemporality selects the aggregation temporality for OTLP metrics.
	// "cumulative" is the default (including an empty or unknown value); "delta"
	// is available for backends that prefer interval measurements.
	MetricTemporality string
	// OutageSummaryInterval controls how often a continuing OTLP export outage is
	// summarized in diagnostics. Zero or negative uses the 5-minute default.
	OutageSummaryInterval time.Duration
	// MetricExportBatchSize bounds each OTLP metric request by datapoint count.
	// The application default is 10000; zero leaves the pinned SDK feature unset
	// for direct package callers.
	MetricExportBatchSize int

	// MaxLogBodyBytes and MaxLogAttrValueBytes bound one log record before
	// export (#366): a single oversized HEC, audit or webhook record must not
	// dominate a batch or breach a backend's per-record limit. Zero leaves the
	// package defaults (32 KiB / 4 KiB) in place, so a direct package caller is
	// bounded without opting in. Truncation is UTF-8 safe, happens after
	// redaction, and never touches metric labels.
	MaxLogBodyBytes      int
	MaxLogAttrValueBytes int

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
	// tailscale2otel.provider attribute on every metric data point, log record and
	// span when non-empty.
	//
	// NOT a Resource attribute, despite what this comment said until #383 fixed
	// it: roadmap item L moved both this and TailnetName off the Resource
	// deliberately (see constLabelAttrs in resource.go). On the Resource they
	// would live only in target_info on Grafana Cloud's OTLP→Prometheus mapping,
	// forcing a join on every query; as signal-scoped attributes they are real
	// labels on every backend, including the Prometheus pull endpoint.
	Provider string

	// TailnetName, when non-empty, is emitted as the tailscale.tailnet attribute on
	// every metric data point, log record and span — a signal-scoped attribute,
	// NOT a Resource attribute (see the Provider field above and constLabelAttrs).
	// The process-level provider leaves it empty; each per-tailnet provider sets
	// it so all of that tailnet's signals carry the dimension. Pair it with a
	// distinct InstanceID per tailnet (see ProviderSet) so each tailnet is its own
	// OTLP target and series don't collide.
	//
	// Subject to the PII policy: an explicitly disabled pii.CatTailnetName omits
	// the attribute, and per-tailnet series then stay distinct via InstanceID
	// alone.
	TailnetName string

	// PIIFilter controls which PII / identifier categories are emitted. A nil map
	// (or a map where every category is true) is a no-op fast path. Set any
	// category to false to drop those identifiers at the emit site.
	PIIFilter pii.Categories

	// StdoutWriter overrides the destination in "stdout" protocol (default os.Stdout).
	StdoutWriter io.Writer

	// The blocks below are the frozen seams for the remaining EPIC-04 (#480)
	// children. Each nested type is DEFINED in the file that owns the behavior it
	// configures, so a lane adds fields to its own struct in its own file rather
	// than contending for this one. A zero value must always mean "behave exactly
	// as before the block existed".

	// Transport tunes the OTLP request itself: compression, timeouts, retry policy,
	// request-size ceiling, gRPC connection options (#360). Defined in exporters.go.
	Transport TransportOptions

	// Signals carries per-signal destination and enablement overrides; an unset
	// signal inherits the common Protocol/Endpoint/Headers/TLS/Transport (#361).
	// Defined in exporters.go.
	Signals SignalOptions

	// Batch tunes the log and span processor queues — capacity, batch size,
	// interval, export timeout — and their drop accounting (#358). Defined in
	// processors.go.
	Batch BatchOptions

	// Stdout makes the "stdout" protocol an immediate debugging sink rather than
	// one on production batching schedules (#384). Defined in processors.go.
	Stdout StdoutOptions

	// Sampling carries per-workload head-sampling classes and the inbound
	// traceparent trust policy (#372, #373). Defined in sampler.go.
	Sampling SamplingOptions

	// Resource carries opt-in standard Resource enrichment — service namespace,
	// deployment environment, bounded custom attributes, controlled
	// OTEL_RESOURCE_ATTRIBUTES (#380). Defined in resource.go.
	Resource ResourceOptions

	// Profiles carries the opt-in Pyroscope span-profile correlation settings
	// (#370). Defined in spanprofile.go.
	Profiles ProfileOptions

	// DynamicHeaders and DynamicTLSConfig, when non-nil, are consulted per
	// REQUEST (headers) and per HANDSHAKE (TLS) instead of the static Headers /
	// CA/Cert/Key fields, so a rotated token or certificate takes effect without
	// restarting the process (#362).
	//
	// They have to be funcs rather than values: the OTLP exporters bake headers
	// and *tls.Config in at construction, so the only way to make either dynamic
	// is to intercept later — a custom http.RoundTripper for HTTP, per-RPC
	// credentials plus tls.Config.GetClientCertificate for gRPC. Both must be
	// cheap and non-blocking; internal/credreload returns a cached immutable
	// snapshot and never touches the filesystem on this path.
	//
	// Nil (the default) keeps the exact static construction, so a deployment that
	// configures no watched file pays nothing and behaves identically.
	DynamicHeaders   func() map[string]string
	DynamicTLSConfig func() *tls.Config

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

	metricCounter *countingMetricExporter // always installed; tallies only under self-obs
	delivery      *deliveryTracker        // per-signal OTLP delivery outcomes (#317)
	logCounter    *countingLogExporter    // nil unless self-obs enabled
	spanCounter   *observingSpanExporter  // nil unless tracing is enabled

	promReg *prometheus.Registry // nil unless Options.PrometheusEnabled

	// batchQueues is the log/span processor queue tracker (#358). Non-nil only
	// under self-observability: the queueing wrappers it needs replace the plain
	// SDK processors, so a deployment that exports no self-telemetry keeps the
	// untouched SDK code path rather than paying for instrumentation nobody reads.
	batchQueues *BatchQueueTracker
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

	// The wrappers are installed unconditionally: they feed the delivery tracker
	// behind the admin page, which has to work on a deployment that exports no
	// self-telemetry at all. Only the data-point/record TALLY is gated on
	// self-obs, since its sole consumer is the self-obs export reporter (#317).
	delivery := newDeliveryTracker(opts.OutageSummaryInterval)
	metricCounter := newCountingMetricExporter(metricExp, opts.SelfObsEnabled, delivery)
	metricExp = metricCounter
	logCounter := newCountingLogExporter(logExp, opts.SelfObsEnabled, delivery)
	logExp = logCounter

	// Built before the processors so newLogProcessor/newSpanProcessor can install
	// their queueing wrappers against it. Gated on self-obs because the wrappers
	// are the only way the SDK's otherwise-unreachable queue depth and drop count
	// become observable at all (see #382: the real counters are private fields on
	// unexported SDK types), and they are not free.
	if opts.SelfObsEnabled && opts.Batch.Tracker == nil {
		opts.Batch.Tracker = NewBatchQueueTracker()
	}
	metricReader, err := newMetricReader(metricExp, opts)
	if err != nil {
		return nil, err
	}
	mpOpts := append(
		metricProviderOptions(metricRes, opts.CardinalityLimit, opts.TracingEnabled),
		sdkmetric.WithReader(metricReader),
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
		sdklog.WithProcessor(newLogProcessor(logExp, opts)),
	)

	var tp *sdktrace.TracerProvider
	var spanCounter *observingSpanExporter
	if opts.TracingEnabled {
		traceExp, err := newTraceExporter(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("trace exporter: %w", err)
		}
		// Spans obey the same PII contract as metrics and logs (#212). This has to
		// wrap the EXPORTER rather than run as a span processor: OnStart is too
		// early (attributes are set afterwards) and OnEnd gets an immutable
		// snapshot — see the piiSpanExporter doc comment in trace.go. A no-op
		// filter (no category disabled) returns traceExp unchanged.
		traceExp = newPIISpanExporter(traceExp, opts.PIIFilter)
		// Outermost, so it times and observes the whole export including the PII
		// filter's work — that is what the backend's latency actually is.
		spanObserver := newObservingSpanExporter(traceExp, delivery)
		spanCounter = spanObserver
		traceExp = spanObserver
		tpOpts := []sdktrace.TracerProviderOption{
			sdktrace.WithResource(logRes),
			sdktrace.WithSpanProcessor(newSpanProcessor(traceExp, opts)),
			sdktrace.WithSampler(newSampler(opts)),
		}
		if len(constAttrs) > 0 {
			tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(constAttrSpanProcessor{attrs: constAttrs}))
		}
		tp = sdktrace.NewTracerProvider(tpOpts...)
	}
	tracer := tracenoop.NewTracerProvider().Tracer(scopeName)
	if tp != nil {
		// The Pyroscope span-profile bridge wraps the TracerProvider so each span
		// stamps pprof labels on its goroutine (#370). Only the TRACER is taken
		// from the wrapper: p.tp stays the real *sdktrace.TracerProvider so
		// ForceFlush and Shutdown keep operating on the SDK directly. A zero
		// ProfileOptions returns tp unchanged, so this is a no-op by default.
		tracer = wrapTracerProviderForProfiles(tp, opts.Profiles).Tracer(scopeName)
	}

	var card *CardinalityTracker
	if opts.SelfObsEnabled {
		card = NewCardinalityTrackerWithLimits(opts.CardinalityLimit, opts.CardinalityLabelValueCap)
	}

	emitter := newOtelEmitter(mp.Meter(scopeName), lp.Logger(scopeName), card, reservedPromotedLabels(opts), opts.Logger, opts.PIIFilter, constAttrs)
	// Set rather than passed: newOtelEmitter already takes seven positional
	// arguments across fourteen call sites, and a zero here correctly resolves to
	// the package defaults.
	emitter.logLimits = logLimits{
		bodyBytes:      opts.MaxLogBodyBytes,
		attrValueBytes: opts.MaxLogAttrValueBytes,
	}

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
		spanCounter:   spanCounter,
		delivery:      delivery,

		promReg:     promReg,
		batchQueues: opts.Batch.Tracker,
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
	// Unlike the metric and log tallies, the span count is NOT gated on self-obs:
	// observingSpanExporter is installed whenever tracing is on (it also feeds the
	// delivery tracker behind the admin page), so the number is already there.
	if p.spanCounter != nil {
		s.Spans = p.spanCounter.count()
	}
	return s
}

// Delivery returns this provider's per-signal OTLP delivery state: what the
// exporters actually shipped, as opposed to what the collectors produced. Always
// populated, self-obs or not.
func (p *Provider) Delivery() []DeliveryState { return p.delivery.states() }

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

// BatchQueues returns the log/span processor queue tracker, or nil when
// self-observability is disabled (no queueing wrappers were installed). The
// caller drives Report on the export interval, mirroring Cardinality.
func (p *Provider) BatchQueues() *BatchQueueTracker { return p.batchQueues }

// Tracer returns the tracer collectors-adjacent infrastructure (scheduler,
// tsapi transport, receivers) records spans with. When tracing is disabled it is
// a no-op tracer, so callers never need to nil-check.
func (p *Provider) Tracer() trace.Tracer { return p.tracer }

// ForceFlush exports pending metrics and logs without stopping either pipeline.
// The pipelines flush concurrently under ctx so one blocked exporter cannot
// consume the shared budget before the other starts. Traces are deliberately
// excluded: this barrier covers ingress-derived metrics and logs only.
func (p *Provider) ForceFlush(ctx context.Context) error {
	return shutdownAll(ctx, p.mp.ForceFlush, p.lp.ForceFlush)
}

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
