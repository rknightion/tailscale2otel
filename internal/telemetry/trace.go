package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/credentials"
)

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

// buildSampler maps the config sampler name + arg to an sdktrace.Sampler.
// Unknown names fall back to the safe default (parentbased_always_on); the
// config layer validates the enum so this fallthrough is defensive only.
func buildSampler(name string, arg float64) sdktrace.Sampler {
	switch name {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(arg)
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(arg))
	default:
		// parentbased_always_on, "" (empty default), and any unvalidated name.
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}
