package app

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard for the
// app layer's self-observability metrics (api.requests, api.retries, and the
// heartbeat up gauge): every metric these helpers actually emit must be declared
// in appcatalog.Catalog() with a matching unit, instrument, and description, so
// the docs generated from it stay honest.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()

	// api.requests (+ api.retries, since attempts > 1).
	apiObserver(rec.Emitter())(context.Background(), "/devices", 200, 3, 75*time.Millisecond)

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

	// runtime.*: emit every gauge and (with non-zero seed values) every counter.
	var last runtimeStats
	emitRuntime(rec.Emitter(), runtimeStats{
		goroutines: 1, gomaxprocs: 1, heapAlloc: 1, heapSys: 1, heapInuse: 1,
		stackInuse: 1, sys: 1, heapObjects: 1, nextGC: 1, gcCPUFraction: 0.5,
		numGC: 1, pauseTotalNs: 1, totalAlloc: 1,
	}, &last)

	// component.errors: emit one so its descriptor is validated too.
	emitComponentError(rec.Emitter(), appcatalog.ComponentStream)

	// admin.auth.rejected: emit one so its descriptor is validated too.
	emitAdminAuthRejected(rec.Emitter(), reasonBadCredentials)

	// dedup.size + dedup.evictions: a cap-1 set with one eviction emits both.
	dset := dedup.New(1)
	dset.Add("a")
	dset.Add("b") // evicts "a" => evictions 1
	emitDedup(rec.Emitter(), map[string]*dedup.Set{"flow": dset}, map[string]uint64{})

	// update_available: emit with a known-newer latest so the gauge fires.
	emitUpdateCheck(rec.Emitter(), func() (string, bool) { return "v9.9.9", true }, "v0.1.0")

	// ingest.records + ingest.bytes via the app closure (self-obs forced on).
	cfgOn := &config.Config{}
	cfgOn.SelfObservability.Enabled = true
	obs := (&App{cfg: cfgOn, emitter: rec.Emitter()}).ingestObserver()
	obs(semconv.IngestSourceStream, semconv.IngestSignalFlow, 3, 256)

	// series.by_group.
	emitSeriesByGroup(rec.Emitter(), map[string]int{"Devices": 2})

	declared := map[string]metricdoc.Metric{}
	for _, m := range appcatalog.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in appcatalog.Catalog()", name)
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
