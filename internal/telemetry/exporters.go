package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
)

// Exporter construction for the three OTLP signals. Split out of provider.go so
// transport concerns (protocol selection, endpoint paths, TLS, headers,
// compression, timeouts, retry) live in one file with one owner.

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
	if !signalEnabled(opts.Signals.Metrics) {
		return noopMetricExporter{}, nil
	}
	opts = resolveSignalOptions(opts, opts.Signals.Metrics)
	compExplicit, compGzip, err := parseCompression(opts.Transport.Compression)
	if err != nil {
		return nil, err
	}
	switch opts.Protocol {
	case "stdout":
		w := opts.StdoutWriter
		if w == nil {
			w = os.Stdout
		}
		mo := []stdoutmetric.Option{stdoutmetric.WithWriter(w)}
		if opts.Stdout.Pretty {
			mo = append(mo, stdoutmetric.WithPrettyPrint())
		}
		return stdoutmetric.New(mo...)
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
		} else if tc, err := effectiveTLSConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlpmetrichttp.WithTLSClientConfig(tc))
		}
		if compExplicit {
			c := otlpmetrichttp.NoCompression
			if compGzip {
				c = otlpmetrichttp.GzipCompression
			}
			o = append(o, otlpmetrichttp.WithCompression(c))
		}
		if opts.Transport.Timeout > 0 {
			o = append(o, otlpmetrichttp.WithTimeout(opts.Transport.Timeout))
		}
		if rp := opts.Transport.Retry; rp != nil {
			o = append(o, otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig(retryConfigFrom(rp))))
		}
		if opts.Transport.MaxRequestSize > 0 {
			o = append(o, otlpmetrichttp.WithMaxRequestSize(opts.Transport.MaxRequestSize))
		}
		// Dynamic credentials replace the transport wholesale, so this must come
		// AFTER the static TLS option above — it supersedes it (#362).
		if hc := dynamicHTTPClient(opts); hc != nil {
			o = append(o, otlpmetrichttp.WithHTTPClient(hc))
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
		} else if tc, err := effectiveTLSConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tc)))
		}
		if compExplicit {
			o = append(o, otlpmetricgrpc.WithCompressor(grpcCompressor(compGzip)))
		}
		if opts.Transport.Timeout > 0 {
			o = append(o, otlpmetricgrpc.WithTimeout(opts.Transport.Timeout))
		}
		if rp := opts.Transport.Retry; rp != nil {
			o = append(o, otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig(retryConfigFrom(rp))))
		}
		if opts.Transport.MaxRequestSize > 0 {
			o = append(o, otlpmetricgrpc.WithMaxRequestSize(opts.Transport.MaxRequestSize))
		}
		if opts.Transport.GRPCReconnectionPeriod > 0 {
			o = append(o, otlpmetricgrpc.WithReconnectionPeriod(opts.Transport.GRPCReconnectionPeriod))
		}
		if dial := dynamicGRPCDialOptions(opts); len(dial) > 0 {
			o = append(o, otlpmetricgrpc.WithDialOption(dial...))
		}
		return otlpmetricgrpc.New(ctx, o...)
	default:
		return nil, fmt.Errorf("unknown otlp protocol %q (want grpc, http, or stdout)", opts.Protocol)
	}
}

func newLogExporter(ctx context.Context, opts Options) (sdklog.Exporter, error) {
	if !signalEnabled(opts.Signals.Logs) {
		return noopLogExporter{}, nil
	}
	opts = resolveSignalOptions(opts, opts.Signals.Logs)
	compExplicit, compGzip, err := parseCompression(opts.Transport.Compression)
	if err != nil {
		return nil, err
	}
	switch opts.Protocol {
	case "stdout":
		w := opts.StdoutWriter
		if w == nil {
			w = os.Stdout
		}
		lo := []stdoutlog.Option{stdoutlog.WithWriter(w)}
		if opts.Stdout.Pretty {
			lo = append(lo, stdoutlog.WithPrettyPrint())
		}
		return stdoutlog.New(lo...)
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
		} else if tc, err := effectiveTLSConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlploghttp.WithTLSClientConfig(tc))
		}
		if compExplicit {
			c := otlploghttp.NoCompression
			if compGzip {
				c = otlploghttp.GzipCompression
			}
			o = append(o, otlploghttp.WithCompression(c))
		}
		if opts.Transport.Timeout > 0 {
			o = append(o, otlploghttp.WithTimeout(opts.Transport.Timeout))
		}
		if rp := opts.Transport.Retry; rp != nil {
			o = append(o, otlploghttp.WithRetry(otlploghttp.RetryConfig(retryConfigFrom(rp))))
		}
		if opts.Transport.MaxRequestSize > 0 {
			o = append(o, otlploghttp.WithMaxRequestSize(opts.Transport.MaxRequestSize))
		}
		// Dynamic credentials replace the transport wholesale, so this must come
		// AFTER the static TLS option above — it supersedes it (#362).
		if hc := dynamicHTTPClient(opts); hc != nil {
			o = append(o, otlploghttp.WithHTTPClient(hc))
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
		} else if tc, err := effectiveTLSConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlploggrpc.WithTLSCredentials(credentials.NewTLS(tc)))
		}
		if compExplicit {
			o = append(o, otlploggrpc.WithCompressor(grpcCompressor(compGzip)))
		}
		if opts.Transport.Timeout > 0 {
			o = append(o, otlploggrpc.WithTimeout(opts.Transport.Timeout))
		}
		if rp := opts.Transport.Retry; rp != nil {
			o = append(o, otlploggrpc.WithRetry(otlploggrpc.RetryConfig(retryConfigFrom(rp))))
		}
		if opts.Transport.MaxRequestSize > 0 {
			o = append(o, otlploggrpc.WithMaxRequestSize(opts.Transport.MaxRequestSize))
		}
		if opts.Transport.GRPCReconnectionPeriod > 0 {
			o = append(o, otlploggrpc.WithReconnectionPeriod(opts.Transport.GRPCReconnectionPeriod))
		}
		if dial := dynamicGRPCDialOptions(opts); len(dial) > 0 {
			o = append(o, otlploggrpc.WithDialOption(dial...))
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
		pem, err := safefile.ReadRegular(opts.CAFile, safefile.MaxPEMBytes, safefile.AllowSymlink)
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
		cert, err := safefile.LoadX509KeyPair(opts.CertFile, opts.KeyFile, safefile.MaxPEMBytes)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// newTraceExporter builds the span exporter for the configured protocol,
// mirroring newMetricExporter/newLogExporter (grpc/http/stdout, same TLS and
// header handling). Traces carry no temporality concept, so unlike the metric
// exporter there is no temporality selector.
func newTraceExporter(ctx context.Context, opts Options) (sdktrace.SpanExporter, error) {
	if !signalEnabled(opts.Signals.Traces) {
		return noopSpanExporter{}, nil
	}
	opts = resolveSignalOptions(opts, opts.Signals.Traces)
	compExplicit, compGzip, err := parseCompression(opts.Transport.Compression)
	if err != nil {
		return nil, err
	}
	switch opts.Protocol {
	case "stdout":
		w := opts.StdoutWriter
		if w == nil {
			w = os.Stdout
		}
		to := []stdouttrace.Option{stdouttrace.WithWriter(w)}
		if opts.Stdout.Pretty {
			to = append(to, stdouttrace.WithPrettyPrint())
		}
		return stdouttrace.New(to...)
	case "", "http":
		o := []otlptracehttp.Option{}
		if opts.Endpoint != "" {
			o = append(o, otlptracehttp.WithEndpointURL(otlpHTTPURL(opts.Endpoint, "traces")))
		}
		if len(opts.Headers) > 0 {
			o = append(o, otlptracehttp.WithHeaders(opts.Headers))
		}
		if opts.Insecure {
			o = append(o, otlptracehttp.WithInsecure())
		} else if tc, err := effectiveTLSConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlptracehttp.WithTLSClientConfig(tc))
		}
		if compExplicit {
			c := otlptracehttp.NoCompression
			if compGzip {
				c = otlptracehttp.GzipCompression
			}
			o = append(o, otlptracehttp.WithCompression(c))
		}
		if opts.Transport.Timeout > 0 {
			o = append(o, otlptracehttp.WithTimeout(opts.Transport.Timeout))
		}
		if rp := opts.Transport.Retry; rp != nil {
			o = append(o, otlptracehttp.WithRetry(otlptracehttp.RetryConfig(retryConfigFrom(rp))))
		}
		if opts.Transport.MaxRequestSize > 0 {
			o = append(o, otlptracehttp.WithMaxRequestSize(opts.Transport.MaxRequestSize))
		}
		// Dynamic credentials replace the transport wholesale, so this must come
		// AFTER the static TLS option above — it supersedes it (#362).
		if hc := dynamicHTTPClient(opts); hc != nil {
			o = append(o, otlptracehttp.WithHTTPClient(hc))
		}
		return otlptracehttp.New(ctx, o...)
	case "grpc":
		o := []otlptracegrpc.Option{}
		if opts.Endpoint != "" {
			o = append(o, otlptracegrpc.WithEndpoint(opts.Endpoint))
		}
		if len(opts.Headers) > 0 {
			o = append(o, otlptracegrpc.WithHeaders(opts.Headers))
		}
		if opts.Insecure {
			o = append(o, otlptracegrpc.WithInsecure())
		} else if tc, err := effectiveTLSConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tc)))
		}
		if compExplicit {
			o = append(o, otlptracegrpc.WithCompressor(grpcCompressor(compGzip)))
		}
		if opts.Transport.Timeout > 0 {
			o = append(o, otlptracegrpc.WithTimeout(opts.Transport.Timeout))
		}
		if rp := opts.Transport.Retry; rp != nil {
			o = append(o, otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig(retryConfigFrom(rp))))
		}
		if opts.Transport.MaxRequestSize > 0 {
			o = append(o, otlptracegrpc.WithMaxRequestSize(opts.Transport.MaxRequestSize))
		}
		if opts.Transport.GRPCReconnectionPeriod > 0 {
			o = append(o, otlptracegrpc.WithReconnectionPeriod(opts.Transport.GRPCReconnectionPeriod))
		}
		if dial := dynamicGRPCDialOptions(opts); len(dial) > 0 {
			o = append(o, otlptracegrpc.WithDialOption(dial...))
		}
		return otlptracegrpc.New(ctx, o...)
	default:
		return nil, fmt.Errorf("unknown otlp protocol %q (want grpc, http, or stdout)", opts.Protocol)
	}
}

// TransportOptions tunes the OTLP request itself (#360): compression, exporter
// timeout, retry policy, request-size ceiling, and gRPC connection options.
// Every zero value leaves the field UNSET rather than forcing a value — for
// Compression and Timeout that means the SDK's own OTEL_EXPORTER_OTLP_* env-var
// resolution (already built into every otlp{metric,log,trace}{http,grpc}
// exporter) still applies exactly as it did before this struct existed. Retry
// and MaxRequestSize have no standard env var, so "unset" there means "the
// exporter's own built-in default" (retry enabled, 5s/30s/1m backoff; no
// request-size ceiling).
type TransportOptions struct {
	// Compression is "" (unset), "gzip", or "none". "" leaves the field unset:
	// the exporter falls back to OTEL_EXPORTER_OTLP_{METRICS,LOGS,TRACES}_
	// COMPRESSION, then OTEL_EXPORTER_OTLP_COMPRESSION, then no compression.
	// An explicit "gzip" or "none" ALWAYS wins over any of those env vars —
	// this is the precedence rule #360/#480 froze. Any other value is a
	// config error (returned from the exporter constructor, not silently
	// ignored).
	Compression string

	// Timeout bounds one export attempt end-to-end (WithTimeout). Zero leaves
	// it unset: OTEL_EXPORTER_OTLP_*_TIMEOUT / OTEL_EXPORTER_OTLP_TIMEOUT
	// still apply, falling back to the exporter's 10s default if neither is
	// set. A positive value always wins over either env var.
	Timeout time.Duration

	// Retry overrides the exporter's retry policy. Nil leaves the exporter's
	// own default policy in place (enabled, 5s initial / 30s max backoff / 1m
	// max elapsed — there is no OTEL_EXPORTER_OTLP_* env var for retry, so
	// there is nothing to fall back to besides that built-in default).
	// Non-nil is taken literally, field by field, including Enabled=false to
	// disable retry outright.
	Retry *RetryPolicy

	// MaxRequestSize caps one serialized export request (before compression),
	// in bytes. Zero means no explicit client-side ceiling. This is a
	// rejection GUARD, not a splitter: it makes an oversized request fail
	// fast and loud client-side instead of shipping and getting a backend 413
	// (Grafana Cloud Mimir's ingest limit is the case that motivated this,
	// see the #360 incident comment) — it does not shrink or split the
	// request. The fix that actually keeps requests under a backend's ingest
	// limit is Options.MetricExportBatchSize (shipped separately, before this
	// lane).
	MaxRequestSize int

	// GRPCReconnectionPeriod bounds how long a gRPC exporter waits before
	// forcing a fresh connection attempt (WithReconnectionPeriod). gRPC-only;
	// silently has no effect on http/stdout. Zero leaves the gRPC client's own
	// default in place.
	GRPCReconnectionPeriod time.Duration
}

// RetryPolicy mirrors the identical RetryConfig shape every otlp{metric,log,
// trace}{http,grpc} exporter package defines (Enabled/InitialInterval/
// MaxInterval/MaxElapsedTime — confirmed identical across all six packages
// against the pinned v1.44.0/v0.20.0 module source, see retryConfigFrom).
type RetryPolicy struct {
	Enabled         bool
	InitialInterval time.Duration
	MaxInterval     time.Duration
	MaxElapsedTime  time.Duration
}

// retryConfigFrom converts a RetryPolicy into the field values shared by every
// exporter package's own RetryConfig(-shaped) type. Callers wrap the return
// value in their package's concrete RetryConfig via a type conversion, since
// Go does not let this helper return six different named types.
func retryConfigFrom(rp *RetryPolicy) struct {
	Enabled         bool
	InitialInterval time.Duration
	MaxInterval     time.Duration
	MaxElapsedTime  time.Duration
} {
	return struct {
		Enabled         bool
		InitialInterval time.Duration
		MaxInterval     time.Duration
		MaxElapsedTime  time.Duration
	}{
		Enabled:         rp.Enabled,
		InitialInterval: rp.InitialInterval,
		MaxInterval:     rp.MaxInterval,
		MaxElapsedTime:  rp.MaxElapsedTime,
	}
}

// parseCompression validates opts.Transport.Compression and reports whether it
// was set explicitly (set==false for "" — the caller must not call any
// WithCompression/WithCompressor option in that case, so the exporter's own
// env-var resolution applies). An unrecognized value is a config error.
func parseCompression(v string) (set bool, gzip bool, err error) {
	switch v {
	case "":
		return false, false, nil
	case "gzip":
		return true, true, nil
	case "none":
		return true, false, nil
	default:
		return false, false, fmt.Errorf("unknown otlp compression %q (want gzip or none)", v)
	}
}

// grpcCompressor renders the shared gRPC WithCompressor(string) argument. The
// gRPC exporters only recognize the literal string "gzip" as a real compressor
// name (internal/oconf's compressorToCompression); any other value — including
// "none" — resolves to NoCompression and logs a benign warning through the
// global otel error handler. That is upstream behavior we cannot silence
// through the public API; calling WithCompressor at all (rather than omitting
// it) is still required to override a competing OTEL_EXPORTER_OTLP_
// *_COMPRESSION=gzip env var, which is the whole point of an explicit "none".
func grpcCompressor(gzipCompression bool) string {
	if gzipCompression {
		return "gzip"
	}
	return "none"
}

// signalEnabled reports whether a signal should export at all. A nil override,
// or one with Enabled left nil, means "on" (the ambient default, matching
// pre-#361 behavior). *bool is required because false must be distinguishable
// from "not set" — a plain bool cannot represent that.
func signalEnabled(ov *SignalOverride) bool {
	if ov == nil || ov.Enabled == nil {
		return true
	}
	return *ov.Enabled
}

// resolveSignalOptions merges a per-signal override onto the common Options,
// returning the effective Options used to build that signal's exporter. A nil
// override returns base completely unchanged — this is what makes a zero-value
// Options.Signals byte-identical to pre-#361 behavior. Every override field is
// compared to its own Go zero value to decide "did the caller set this",
// EXCEPT Headers (nil map = inherit; a non-nil map, even empty, REPLACES the
// common Headers wholesale rather than merging) and Insecure/
// InsecureSkipVerify (*bool, same "unset vs explicit false" reasoning as
// Enabled). Headers replace rather than merge specifically so a credential set
// only on one signal's override can never leak onto another signal that
// inherits the common block (#361: "credentials must never cross signals").
func resolveSignalOptions(base Options, ov *SignalOverride) Options {
	if ov == nil {
		return base
	}
	eff := base
	if ov.Protocol != "" {
		eff.Protocol = ov.Protocol
	}
	if ov.Endpoint != "" {
		eff.Endpoint = ov.Endpoint
	}
	if ov.Headers != nil {
		eff.Headers = ov.Headers
	}
	if ov.Insecure != nil {
		eff.Insecure = *ov.Insecure
	}
	if ov.InsecureSkipVerify != nil {
		eff.InsecureSkipVerify = *ov.InsecureSkipVerify
	}
	if ov.CAFile != "" {
		eff.CAFile = ov.CAFile
	}
	if ov.CertFile != "" {
		eff.CertFile = ov.CertFile
	}
	if ov.KeyFile != "" {
		eff.KeyFile = ov.KeyFile
	}
	if ov.Transport != nil {
		eff.Transport = *ov.Transport
	}
	return eff
}

// SignalOverride carries one signal's (metrics, logs, or traces) destination,
// transport, and enablement overrides (#361). A nil *SignalOverride on
// SignalOptions means that signal fully inherits the common Options fields —
// the identical exporter pre-#361 code built.
type SignalOverride struct {
	// Enabled explicitly turns this signal's export on or off. Nil inherits
	// the ambient default (on). *bool because false must be distinguishable
	// from "not set".
	Enabled *bool

	// Protocol/Endpoint mirror the identically-named Options fields; "" means
	// inherit the common value.
	Protocol string
	Endpoint string

	// Headers replaces (never merges with) the common Options.Headers when
	// non-nil, specifically so a credential set on one signal's override can
	// never reach another signal. Nil inherits the common Headers unchanged.
	Headers map[string]string

	// Insecure/InsecureSkipVerify mirror the identically-named Options
	// fields; nil inherits the common value (see the Enabled doc comment for
	// why these are *bool rather than bool).
	Insecure           *bool
	InsecureSkipVerify *bool

	// CAFile/CertFile/KeyFile mirror the identically-named Options fields;
	// "" inherits the common value.
	CAFile   string
	CertFile string
	KeyFile  string

	// Transport overrides the common Options.Transport wholesale for this
	// signal when non-nil. Nil inherits the common Options.Transport
	// unchanged.
	Transport *TransportOptions
}

// SignalOptions carries per-signal destination and enablement overrides
// (#361). A zero value means every signal inherits the common
// Protocol/Endpoint/Headers/TLS/Transport settings — old configuration
// behavior is byte-identical, since every exporter constructor's
// resolveSignalOptions(base, nil) returns base unchanged.
type SignalOptions struct {
	Metrics *SignalOverride
	Logs    *SignalOverride
	Traces  *SignalOverride
}

// noopMetricExporter discards every export. Installed when Signals.Metrics.
// Enabled is explicitly false (#361), so a disabled signal never opens a
// network connection or serializes a request at all.
type noopMetricExporter struct{}

func (noopMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}

func (noopMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (noopMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error { return nil }
func (noopMetricExporter) ForceFlush(context.Context) error                          { return nil }
func (noopMetricExporter) Shutdown(context.Context) error                            { return nil }

// noopLogExporter discards every log record. Installed when Signals.Logs.
// Enabled is explicitly false (#361) — the practical equivalent of "a no-op
// LoggerProvider" from inside this file's scope: no record is ever
// serialized or sent, without requiring a change to how provider.go builds
// the LoggerProvider itself.
type noopLogExporter struct{}

func (noopLogExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (noopLogExporter) Shutdown(context.Context) error                { return nil }
func (noopLogExporter) ForceFlush(context.Context) error              { return nil }

// noopSpanExporter discards every span. Installed when Signals.Traces.Enabled
// is explicitly false (#361).
type noopSpanExporter struct{}

func (noopSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (noopSpanExporter) Shutdown(context.Context) error                             { return nil }

// EffectiveTransportSettings is the non-secret, resolved subset of
// TransportOptions for one signal, safe to render on the admin status page
// (#360/#361). It has no field capable of holding a header, token, or key
// bytes by construction — there is nothing to accidentally leak.
type EffectiveTransportSettings struct {
	// Compression is the resolved value ("gzip"/"none") or "" when left unset
	// (the SDK's own OTEL_EXPORTER_OTLP_* env-var resolution applies, and
	// which env var — if any — actually took effect is not observable from
	// here).
	Compression string
	Timeout     time.Duration

	// RetryEnabled is nil when Transport.Retry itself is unset (the
	// exporter's own default retry policy applies); non-nil mirrors the
	// resolved RetryPolicy.Enabled.
	RetryEnabled           *bool
	RetryInitialInterval   time.Duration
	RetryMaxInterval       time.Duration
	RetryMaxElapsedTime    time.Duration
	MaxRequestSize         int
	GRPCReconnectionPeriod time.Duration
}

// EffectiveSignalSettings is the non-secret, resolved effective configuration
// for one signal (metrics, logs, or traces) after applying its SignalOverride
// (if any) onto the common Options. Deliberately excludes Headers, and
// CAFile/CertFile/KeyFile carry only the configured filesystem PATH, never
// file contents.
type EffectiveSignalSettings struct {
	Enabled            bool
	Protocol           string
	Endpoint           string
	Insecure           bool
	InsecureSkipVerify bool
	CAFile             string
	CertFile           string
	KeyFile            string
	Transport          EffectiveTransportSettings
}

// EffectiveSettings is the resolved, non-secret effective settings for all
// three signals, as returned by ResolveEffectiveSettings.
type EffectiveSettings struct {
	Metrics EffectiveSignalSettings
	Logs    EffectiveSignalSettings
	Traces  EffectiveSignalSettings
}

// ResolveEffectiveSettings computes the resolved, non-secret effective
// settings for all three signals from the same Options a Provider would be
// (or was) built from. It is a pure function over Options rather than a
// Provider method so callers that only retain the Options they built a
// Provider from (e.g. the admin status page) can call it directly without any
// change to Provider itself. Never returns Headers or any field that could
// carry a credential.
func ResolveEffectiveSettings(opts Options) EffectiveSettings {
	return EffectiveSettings{
		Metrics: effectiveSignalSettings(opts, opts.Signals.Metrics),
		Logs:    effectiveSignalSettings(opts, opts.Signals.Logs),
		Traces:  effectiveSignalSettings(opts, opts.Signals.Traces),
	}
}

func effectiveSignalSettings(base Options, ov *SignalOverride) EffectiveSignalSettings {
	eff := resolveSignalOptions(base, ov)
	s := EffectiveSignalSettings{
		Enabled:            signalEnabled(ov),
		Protocol:           eff.Protocol,
		Endpoint:           eff.Endpoint,
		Insecure:           eff.Insecure,
		InsecureSkipVerify: eff.InsecureSkipVerify,
		CAFile:             eff.CAFile,
		CertFile:           eff.CertFile,
		KeyFile:            eff.KeyFile,
		Transport: EffectiveTransportSettings{
			Compression:            eff.Transport.Compression,
			Timeout:                eff.Transport.Timeout,
			MaxRequestSize:         eff.Transport.MaxRequestSize,
			GRPCReconnectionPeriod: eff.Transport.GRPCReconnectionPeriod,
		},
	}
	if rp := eff.Transport.Retry; rp != nil {
		enabled := rp.Enabled
		s.Transport.RetryEnabled = &enabled
		s.Transport.RetryInitialInterval = rp.InitialInterval
		s.Transport.RetryMaxInterval = rp.MaxInterval
		s.Transport.RetryMaxElapsedTime = rp.MaxElapsedTime
	}
	return s
}

// Dynamic outbound credentials (#362). A rotated bearer token, header value or
// client certificate must take effect without restarting the process, but the
// OTLP exporters bake headers and *tls.Config in at construction — so the only
// place to make either dynamic is after construction:
//
//   - HTTP: a custom http.RoundTripper injected via WithHTTPClient sets the
//     headers per request and dials with a freshly-read *tls.Config, so a
//     rotated CA takes effect on the next new connection.
//   - gRPC: credentials.PerRPCCredentials supplies headers per RPC on the
//     existing connection, and tls.Config.GetClientCertificate is consulted per
//     handshake so a rotated client cert needs no reconnect.
//
// One asymmetry is deliberate and worth stating rather than papering over: on
// gRPC the ROOT CAs are fixed when the connection is dialed. A rotated CA
// bundle therefore only takes effect when gRPC next reconnects (which
// otlp.grpc_reconnection_period can bound). Client-certificate and header
// rotation are immediate on both transports.

// dynamicHeaderTransport sets the current headers on every request before
// delegating. It never caches them: the point is that the value may have changed
// since the last request.
type dynamicHeaderTransport struct {
	base    http.RoundTripper
	headers func() map[string]string
}

func (d *dynamicHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone before mutating: RoundTrippers must not modify the caller's request,
	// and the SDK reuses request objects across retry attempts.
	r := req.Clone(req.Context())
	for k, v := range d.headers() {
		r.Header.Set(k, v)
	}
	return d.base.RoundTrip(r)
}

// dynamicPerRPCCredentials supplies the current headers as gRPC metadata on
// every RPC.
type dynamicPerRPCCredentials struct {
	headers func() map[string]string
	// secure reports whether the channel is encrypted. gRPC refuses to send
	// credentials over a plaintext channel unless they say they do not require
	// transport security, which is the correct default: a bearer token on h2c
	// would go out in the clear.
	secure bool
}

func (d dynamicPerRPCCredentials) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return d.headers(), nil
}

func (d dynamicPerRPCCredentials) RequireTransportSecurity() bool { return d.secure }

// dynamicHTTPClient builds the *http.Client the OTLP/HTTP exporters should use
// when either credentials source is dynamic, or nil when neither is.
//
// The transport dials with a per-connection *tls.Config read at dial time, so a
// rotated CA or client keypair applies to every new connection without a
// restart. Timeouts are left to the exporter's own WithTimeout rather than set
// here, so this wrapper never silently overrides a configured timeout.
func dynamicHTTPClient(opts Options) *http.Client {
	if opts.DynamicHeaders == nil && opts.DynamicTLSConfig == nil {
		return nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if opts.DynamicTLSConfig != nil {
		tlsFn := opts.DynamicTLSConfig
		tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			cfg := tlsFn()
			if cfg == nil {
				cfg = &tls.Config{MinVersion: tls.VersionTLS12}
			} else {
				cfg = cfg.Clone()
			}
			if cfg.ServerName == "" {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					// No port to strip; use the address as the SNI name. Leaving
					// ServerName empty would disable verification of the hostname.
					host = addr
				}
				cfg.ServerName = host
			}
			d := &net.Dialer{Timeout: 30 * time.Second}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			conn := tls.Client(raw, cfg)
			if err := conn.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				return nil, err
			}
			return conn, nil
		}
	}
	var rt http.RoundTripper = tr
	if opts.DynamicHeaders != nil {
		rt = &dynamicHeaderTransport{base: tr, headers: opts.DynamicHeaders}
	}
	return &http.Client{Transport: rt}
}

// dynamicGRPCDialOptions returns the dial options that make gRPC credentials
// dynamic, or nil when nothing is.
func dynamicGRPCDialOptions(opts Options) []grpc.DialOption {
	if opts.DynamicHeaders == nil {
		return nil
	}
	return []grpc.DialOption{grpc.WithPerRPCCredentials(dynamicPerRPCCredentials{
		headers: opts.DynamicHeaders,
		// Plaintext (h2c) must NOT claim to require transport security, or gRPC
		// refuses the RPC outright; but on a plaintext channel the operator has
		// already chosen to send everything in the clear.
		secure: !opts.Insecure,
	})}
}

// effectiveTLSConfig returns the TLS config an exporter should be constructed
// with: the dynamic one when configured, otherwise the static file-derived one.
// It exists so the six exporter construction sites share one decision.
func effectiveTLSConfig(opts Options) (*tls.Config, error) {
	if opts.DynamicTLSConfig != nil {
		return opts.DynamicTLSConfig(), nil
	}
	return tlsConfig(opts)
}
