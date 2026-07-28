package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

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
	"google.golang.org/grpc/credentials"
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

// newTraceExporter builds the span exporter for the configured protocol,
// mirroring newMetricExporter/newLogExporter (grpc/http/stdout, same TLS and
// header handling). Traces carry no temporality concept, so unlike the metric
// exporter there is no temporality selector.
func newTraceExporter(ctx context.Context, opts Options) (sdktrace.SpanExporter, error) {
	switch opts.Protocol {
	case "stdout":
		w := opts.StdoutWriter
		if w == nil {
			w = os.Stdout
		}
		return stdouttrace.New(stdouttrace.WithWriter(w))
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
		} else if tc, err := tlsConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlptracehttp.WithTLSClientConfig(tc))
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
		} else if tc, err := tlsConfig(opts); err != nil {
			return nil, err
		} else if tc != nil {
			o = append(o, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tc)))
		}
		return otlptracegrpc.New(ctx, o...)
	default:
		return nil, fmt.Errorf("unknown otlp protocol %q (want grpc, http, or stdout)", opts.Protocol)
	}
}

// TransportOptions tunes the OTLP request itself (#360). Owned by the #360/#361
// lane. Every zero value must reproduce the pre-#360 behavior: the SDK default.
type TransportOptions struct{}

// SignalOptions carries per-signal destination and enablement overrides (#361).
// Owned by the #360/#361 lane. A zero value means every signal inherits the
// common Protocol/Endpoint/Headers/TLS/Transport settings.
type SignalOptions struct{}
