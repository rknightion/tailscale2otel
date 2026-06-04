package telemetry_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
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
