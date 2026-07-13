package collector_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard for the
// scheduler's per-collector scrape.* self-observability metrics: a failing
// collector exercises all four (duration, success, last_timestamp, errors), and
// every emitted metric must be declared in collector.Catalog() with a matching
// unit, instrument, and description (docs/metrics.md is generated from Catalog()).
func TestCatalogMatchesEmitted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "bad", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		return errors.New("boom")
	}}, time.Millisecond)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now)

	// Wait until the failing run has emitted the errors counter; by then the
	// duration/success/last_timestamp gauges for the same run are present too.
	waitFor(t, func() bool {
		p, ok := findPoint(rec, collector.MetricScrapeErrors, "bad")
		return ok && p.Value >= 1
	}, 2*time.Second)

	// A second scenario exercises checkpoint.persist.errors: a window collector
	// that collects fine but whose high-water mark cannot be saved.
	rWin := collector.NewRegistry()
	rWin.RegisterWindow(winFunc{name: "win", def: time.Millisecond, lag: time.Minute,
		fn: func(_ context.Context, _, to time.Time, _ telemetry.Emitter) (time.Time, error) {
			return to, nil
		}}, time.Millisecond, 5*time.Minute, time.Hour)
	recWin := telemetrytest.New()
	runRecorderSchedulerStore(t, rWin, recWin, now, errSetStore{})
	waitFor(t, func() bool {
		p, ok := findPoint(recWin, collector.MetricCheckpointPersistErrors, "win")
		return ok && p.Value >= 1
	}, 2*time.Second)

	declared := map[string]metricdoc.Metric{}
	for _, m := range collector.Catalog() {
		declared[m.Name] = m
	}

	assertMatches := func(rec *telemetrytest.Recorder) {
		for _, name := range rec.MetricNames() {
			pts := rec.MetricPoints(name)
			if len(pts) == 0 {
				continue
			}
			p0 := pts[0]
			d, ok := declared[name]
			if !ok {
				t.Errorf("emitted metric %q is not declared in collector.Catalog()", name)
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
	assertMatches(rec)
	assertMatches(recWin)
}
