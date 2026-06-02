package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
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
	"go.opentelemetry.io/otel/sdk/resource"
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

	// StdoutWriter overrides the destination in "stdout" protocol (default os.Stdout).
	StdoutWriter io.Writer
}

// Provider owns the OTEL MeterProvider and LoggerProvider and exposes a single
// Emitter for collectors. Shutdown flushes and releases both.
type Provider struct {
	mp      *sdkmetric.MeterProvider
	lp      *sdklog.LoggerProvider
	emitter Emitter
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

	interval := opts.MetricInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(interval))),
	)
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
	)

	return &Provider{
		mp:      mp,
		lp:      lp,
		emitter: NewEmitter(mp.Meter(scopeName), lp.Logger(scopeName)),
	}, nil
}

// Emitter returns the Emitter collectors should use.
func (p *Provider) Emitter() Emitter { return p.emitter }

// Shutdown flushes and stops the metric and log pipelines.
func (p *Provider) Shutdown(ctx context.Context) error {
	return errors.Join(p.mp.Shutdown(ctx), p.lp.Shutdown(ctx))
}

func buildResource(ctx context.Context, opts Options) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{attribute.String("service.name", opts.ServiceName)}
	if opts.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", opts.ServiceVersion))
	}
	if opts.InstanceID != "" {
		attrs = append(attrs, attribute.String("service.instance.id", opts.InstanceID))
	}
	return resource.New(ctx, resource.WithAttributes(attrs...), resource.WithTelemetrySDK())
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

func newMetricExporter(ctx context.Context, opts Options) (sdkmetric.Exporter, error) {
	switch opts.Protocol {
	case "stdout":
		w := opts.StdoutWriter
		if w == nil {
			w = os.Stdout
		}
		return stdoutmetric.New(stdoutmetric.WithWriter(w))
	case "", "http":
		o := []otlpmetrichttp.Option{}
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
		o := []otlpmetricgrpc.Option{}
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
	cfg := &tls.Config{}
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
