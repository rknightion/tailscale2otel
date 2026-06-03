package app

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard for the
// app layer's self-observability metrics (api.requests, api.retries, and the
// heartbeat up gauge): every metric these helpers actually emit must be declared
// in app.Catalog() with a matching unit, instrument, and description, so the
// docs generated from Catalog() stay honest.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()

	// api.requests (+ api.retries, since attempts > 1).
	apiObserver(rec.Emitter())("/devices", 200, 3)

	// up: runHeartbeat emits once immediately, before blocking on its ticker.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runHeartbeat(ctx, rec.Emitter(), time.Hour)
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.MetricPoints("tailscale2otel.up")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("heartbeat never emitted tailscale2otel.up")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	declared := map[string]metricdoc.Metric{}
	for _, m := range Catalog() {
		declared[m.Name] = m
	}

	for _, name := range rec.MetricNames() {
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in app.Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
		wantCounter := d.Instrument == metricdoc.Counter
		gotCounter := p0.Kind == "sum" && p0.Monotonic
		if wantCounter != gotCounter {
			t.Errorf("%s: catalog instrument %q but emitted kind=%q monotonic=%v", name, d.Instrument, p0.Kind, p0.Monotonic)
		}
	}
}
