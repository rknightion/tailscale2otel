package telemetry_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

func TestProvider_StdoutFlushesMetricOnShutdown(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		ServiceVersion: "test",
		Protocol:       "stdout",
		StdoutWriter:   &buf,
		MetricInterval: time.Hour, // rely on Shutdown to flush, not the interval
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	p.Emitter().Counter("tailscale.test.counter", "1", "", 1, nil)
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !strings.Contains(buf.String(), "tailscale.test.counter") {
		t.Fatalf("stdout output missing metric name; got:\n%s", buf.String())
	}
}

// TestProvider_MetricsResourceOmitsServiceVersion asserts service.version is NOT
// attached to the metrics resource, while the logs resource DOES keep it.
//
// Grafana Cloud's OTLP ingest promotes service.* resource attributes to per-series
// labels, so a service.version on the metrics resource becomes a service_version
// label on every series — a per-build value on every series, which mints a fresh
// series set on each redeploy (#187; the doubling symptom in graph2otel#104). The
// stdout exporter prints each signal's Resource block, so a metrics-only flush must
// not mention service.version, and a run that emits a log must.
func TestProvider_MetricsResourceOmitsServiceVersion(t *testing.T) {
	ctx := context.Background()

	// Metrics-only flush: the log batch processor has nothing to emit, so the
	// buffer holds only ResourceMetrics — which must not carry service.version.
	var metricsBuf bytes.Buffer
	pm, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		ServiceVersion: "v1.2.3-abcdef",
		Protocol:       "stdout",
		StdoutWriter:   &metricsBuf,
		MetricInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	pm.Emitter().Counter("tailscale.test.counter", "1", "", 1, nil)
	if err := pm.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if strings.Contains(metricsBuf.String(), "service.version") {
		t.Fatalf("metrics resource carries service.version (#187 regression); got:\n%s", metricsBuf.String())
	}

	// A run that emits a log record must carry service.version on the logs
	// resource (logs are never summed, so per-record version attribution is safe).
	var logBuf bytes.Buffer
	pl, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		ServiceVersion: "v1.2.3-abcdef",
		Protocol:       "stdout",
		StdoutWriter:   &logBuf,
		MetricInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	pl.Emitter().LogEvent(telemetry.Event{Name: "tailscale.test", Body: "hi"})
	if err := pl.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !strings.Contains(logBuf.String(), "service.version") {
		t.Fatalf("logs resource is missing service.version; got:\n%s", logBuf.String())
	}
}

// TestProvider_AppliesCardinalityLimit asserts the configured per-instrument
// cardinality limit reaches the MeterProvider: emitting more distinct attribute
// sets than the limit produces the SDK's otel.metric.overflow series. Without the
// limit wired through, three series stay well under the SDK default (2000) and no
// overflow appears, so this fails unless the limit is applied.
func TestProvider_AppliesCardinalityLimit(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:      "tailscale2otel",
		ServiceVersion:   "test",
		Protocol:         "stdout",
		StdoutWriter:     &buf,
		MetricInterval:   time.Hour,
		CardinalityLimit: 2,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		p.Emitter().Counter("tailscale.test.counter", "1", "", 1, telemetry.Attrs{"id": id})
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !strings.Contains(buf.String(), "otel.metric.overflow") {
		t.Fatalf("expected otel.metric.overflow series with cardinality limit 2; got:\n%s", buf.String())
	}
}

func TestProvider_InvalidProtocolErrors(t *testing.T) {
	if _, err := telemetry.NewProvider(context.Background(), telemetry.Options{
		ServiceName: "x",
		Protocol:    "bogus",
	}); err == nil {
		t.Fatal("expected error for invalid protocol, got nil")
	}
}

func TestProvider_HTTPConstructs(t *testing.T) {
	// Construction must not dial; it should succeed without a live endpoint.
	p, err := telemetry.NewProvider(context.Background(), telemetry.Options{
		ServiceName: "tailscale2otel",
		Protocol:    "http",
		Endpoint:    "https://otlp-gateway-prod-us-central-0.grafana.net/otlp",
		Headers:     map[string]string{"Authorization": "Basic deadbeef"},
	})
	if err != nil {
		t.Fatalf("NewProvider(http): %v", err)
	}
	if p.Emitter() == nil {
		t.Fatal("Emitter() returned nil")
	}
	_ = p.Shutdown(context.Background())
}

func TestProvider_TracerNoopWhenDisabled(t *testing.T) {
	p, err := telemetry.NewProvider(context.Background(), telemetry.Options{
		ServiceName: "t", Protocol: "stdout", StdoutWriter: io.Discard,
		TracingEnabled: false,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	_, span := p.Tracer().Start(context.Background(), "probe")
	if span.SpanContext().IsValid() {
		t.Error("disabled tracing must yield a no-op tracer (invalid span context)")
	}
	span.End()
}

func TestProvider_TracerRecordsWhenEnabled(t *testing.T) {
	p, err := telemetry.NewProvider(context.Background(), telemetry.Options{
		ServiceName: "t", Protocol: "stdout", StdoutWriter: io.Discard,
		TracingEnabled: true, TraceSampler: "always_on",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	_, span := p.Tracer().Start(context.Background(), "probe")
	if !span.SpanContext().IsSampled() {
		t.Error("enabled tracing with always_on must produce a sampled span")
	}
	span.End()
}

// TestProvider_ShutdownFlushesSpans is the trace analog of
// TestProvider_StdoutFlushesMetricOnShutdown: the batch span processor must
// flush ended spans to the stdout exporter on Shutdown, not only on its timer.
func TestProvider_ShutdownFlushesSpans(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		ServiceVersion: "test",
		Protocol:       "stdout",
		StdoutWriter:   &buf,
		TracingEnabled: true,
		TraceSampler:   "always_on",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	_, span := p.Tracer().Start(ctx, "tailscale.test.span")
	span.End()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !strings.Contains(buf.String(), "tailscale.test.span") {
		t.Fatalf("stdout output missing span name; got:\n%s", buf.String())
	}
}
