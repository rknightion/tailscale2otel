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
	"time"

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

	Insecure bool
	CAFile   string
	CertFile string
	KeyFile  string

	MetricInterval time.Duration // PeriodicReader interval (default 60s)

	// CardinalityLimit is the hard per-instrument limit on the number of distinct
	// attribute sets collected per cycle; sets beyond it collapse into the SDK's
	// otel_metric_overflow series. 0 or negative means unlimited. The app layer
	// supplies the configured default (10000); the same value caps the
	// self-observability series tracker so series.active pins exactly at the limit.
	CardinalityLimit int

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

	// Provider is the control-plane backend (tailscale|headscale); emitted as the
	// tailscale2otel.provider resource attribute when non-empty.
	Provider string

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
}

// metricProviderOptions returns the MeterProvider options shared by the production
// pipeline and tests — everything except the reader, which differs (a PeriodicReader
// in production, a ManualReader in tests). Centralizing them here lets the
// cardinality-limit and exemplar-filter behavior be asserted against an in-memory
// reader without duplicating the wiring.
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
	return []sdkmetric.Option{
		sdkmetric.WithResource(res),
		// Hard per-instrument cardinality limit (0/neg = unlimited). Raises the SDK
		// default of 2000 to whatever the app configures (default 10000); beyond it
		// the SDK emits otel_metric_overflow.
		sdkmetric.WithCardinalityLimit(cardinalityLimit),
		sdkmetric.WithExemplarFilter(exemplarFilter),
	}
}

// NewProvider builds the telemetry pipeline for the given options.
func NewProvider(ctx context.Context, opts Options) (*Provider, error) {
	res, err := buildResource(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}
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
	mp := sdkmetric.NewMeterProvider(append(
		metricProviderOptions(res, opts.CardinalityLimit, opts.TracingEnabled),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(interval))),
	)...)
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
	)

	var tp *sdktrace.TracerProvider
	if opts.TracingEnabled {
		traceExp, err := newTraceExporter(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("trace exporter: %w", err)
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithSampler(buildSampler(opts.TraceSampler, opts.TraceSamplerArg)),
		)
	}
	tracer := tracenoop.NewTracerProvider().Tracer(scopeName)
	if tp != nil {
		tracer = tp.Tracer(scopeName)
	}

	var card *CardinalityTracker
	if opts.SelfObsEnabled {
		card = NewCardinalityTrackerWithCap(opts.CardinalityLimit)
	}

	emitter := newOtelEmitter(mp.Meter(scopeName), lp.Logger(scopeName), card, reservedPromotedLabels(opts), opts.Logger)

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

// Cardinality returns the self-observability cardinality tracker, or nil when
// self-observability is disabled. The caller drives Report on the export
// interval and may call Report safely even when this is nil.
func (p *Provider) Cardinality() *CardinalityTracker { return p.card }

// Tracer returns the tracer collectors-adjacent infrastructure (scheduler,
// tsapi transport, receivers) records spans with. When tracing is disabled it is
// a no-op tracer, so callers never need to nil-check.
func (p *Provider) Tracer() trace.Tracer { return p.tracer }

// Shutdown flushes and stops the metric, log, and trace pipelines.
func (p *Provider) Shutdown(ctx context.Context) error {
	errs := []error{p.mp.Shutdown(ctx), p.lp.Shutdown(ctx)}
	if p.tp != nil {
		errs = append(errs, p.tp.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

func buildResource(ctx context.Context, opts Options) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{attribute.String("service.name", opts.ServiceName)}
	if opts.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", opts.ServiceVersion))
	}
	if opts.InstanceID != "" {
		attrs = append(attrs, attribute.String("service.instance.id", opts.InstanceID))
	}
	if opts.Provider != "" {
		attrs = append(attrs, attribute.String("tailscale2otel.provider", opts.Provider))
	}
	// The schemaless WithAttributes block carries the service.* identity; the core
	// detectors add host/os/process attributes so multiple instances are
	// distinguishable in Grafana. All detectors share one semconv schema URL, so
	// merging them with the schemaless block cannot raise a schema-URL conflict.
	// A narrow process subset is used deliberately — WithProcess() would also
	// emit process.command_args and process.owner, which can leak deploy paths
	// and usernames to the backend.
	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
	)
	// A partial resource (a detector that couldn't read its source — e.g.
	// os.Hostname() failing) must NOT abort startup: the exporter's core job is
	// unaffected, so continue with whatever attributes were resolved. Any other
	// error (which, given the shared schema URL, should not occur) is fatal.
	if err != nil && errors.Is(err, resource.ErrPartialResource) {
		return res, nil
	}
	return res, err
}

// reservedPromotedLabels returns the Prometheus label names that Grafana Cloud
// promotes from the OTEL Resource onto every exported series: service.name→job,
// service.instance.id→instance, plus the service_* labels (confirmed on live
// series). A data-point attribute that normalizes to one of these would duplicate
// the promoted label and get the whole sample rejected as otlp_parse_error, so the
// Emitter drops it (the resource value wins). Host/OS/process resource attributes
// are deliberately NOT reserved — Grafana keeps those in target_info only, so a
// data-point host.name (e.g. the node-metrics passthrough) does not collide.
func reservedPromotedLabels(opts Options) map[string]struct{} {
	r := map[string]struct{}{
		"job":      {},
		"instance": {},
	}
	if opts.ServiceName != "" {
		r["service_name"] = struct{}{}
	}
	if opts.ServiceVersion != "" {
		r["service_version"] = struct{}{}
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
	if opts.CAFile == "" && opts.CertFile == "" && opts.KeyFile == "" {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
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
