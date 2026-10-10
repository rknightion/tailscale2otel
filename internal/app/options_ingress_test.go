package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestTelemetryOptionsIngressPermanentLossMapping(t *testing.T) {
	cfg := config.Default()
	cfg.IngressWAL.MaxPermanentRejections = 2
	cfg.SelfObservability.Enabled = false // Explicit loss must not silently disappear.
	opts := telemetryOptions(cfg, "fixture")
	if opts.MaxPermanentRejections != 2 || opts.OnPermanentDrop == nil {
		t.Fatal("initialization-time delivery policy not wired")
	}
	rec := telemetrytest.New()
	for _, signal := range []string{telemetry.SignalMetrics, telemetry.SignalLogs} {
		opts.OnPermanentDrop(rec.Emitter(), signal)
	}
	points := rec.MetricPoints(appcatalog.MetricIngressWALPermanentDrops)
	if len(points) != 2 {
		t.Fatalf("loss points=%v", points)
	}
	remaining := map[string]bool{telemetry.SignalMetrics: true, telemetry.SignalLogs: true}
	for _, p := range points {
		signal := p.Attrs[semconv.AttrIngestSignal]
		if !remaining[signal] {
			t.Fatalf("unexpected/duplicate loss signal: %+v", p)
		}
		delete(remaining, signal)
		if p.Value != 1 || !p.Monotonic || p.Kind != "sum" {
			t.Fatalf("loss point=%+v", p)
		}
		if p.Description != appcatalog.DocIngressWALPermanentDrops.Description || p.Unit != appcatalog.DocIngressWALPermanentDrops.Unit {
			t.Fatalf("loss descriptor drift=%+v", p)
		}
	}
	if len(remaining) != 0 {
		t.Fatalf("missing loss signals: %v", remaining)
	}
	telemetrytest.AssertCatalogAttrs(t, rec, appcatalog.Catalog(), nil)
}
