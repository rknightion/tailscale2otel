package app

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// A non-positive metric interval (e.g. otlp.metric_interval: 0s) must not crash
// the cardinality reporter: time.NewTicker(0) panics, so the reporter has to
// clamp to a positive fallback the way its sibling reporters (runtime, dedup)
// do. With a pre-canceled context the reporter should create its ticker and
// then return on ctx.Done() rather than panic.
func TestRunCardinalityReporter_NonPositiveIntervalDoesNotPanic(t *testing.T) {
	rec := telemetrytest.New()
	card := telemetry.NewCardinalityTracker() // non-nil tracker => reporter proceeds past the nil guard

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runCardinalityReporter(ctx, rec.Emitter(), card, nil, 0)
}

// TestReportCycleEmitsByGroup checks one report tick emits both the per-metric
// series.active gauge (via the tracker) and the rolled-up series.by_group gauge.
func TestReportCycleEmitsByGroup(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()
	tr.Observe("tailscale.device.online", telemetry.Attrs{"id": "a"})
	tr.Observe("tailscale.device.online", telemetry.Attrs{"id": "b"})
	tr.Observe("tailscale.network.flow", telemetry.Attrs{"id": "c"})

	reportCardinalityCycle(rec.Emitter(), tr, map[string]string{
		"tailscale.device.online": "Devices",
		"tailscale.network.flow":  "Network",
	})

	if len(rec.MetricPoints("tailscale2otel.series.active")) == 0 {
		t.Fatal("series.active not emitted")
	}
	if got := len(rec.MetricPoints(appcatalog.MetricSeriesByGroup)); got != 2 {
		t.Fatalf("series.by_group points = %d, want 2", got)
	}
}

// TestRollupSeriesByGroup checks that per-metric active-series counts are summed
// by their catalog group, and that any metric name absent from the group map
// buckets under "other".
func TestRollupSeriesByGroup(t *testing.T) {
	groups := map[string]string{
		"device.online": "Devices",
		"devices.count": "Devices",
		"network.flow":  "Network",
	}
	snap := []telemetry.SeriesCount{
		{Metric: "device.online", Count: 4},
		{Metric: "devices.count", Count: 2},
		{Metric: "network.flow", Count: 9},
		{Metric: "uncataloged.thing", Count: 2},
	}
	got := rollupSeriesByGroup(snap, groups)
	want := map[string]int{"Devices": 6, "Network": 9, "other": 2}
	if len(got) != len(want) {
		t.Fatalf("rollup groups = %v, want %v", got, want)
	}
	for g, n := range want {
		if got[g] != n {
			t.Errorf("rollup[%q] = %d, want %d", g, got[g], n)
		}
	}
}

// TestEmitSeriesByGroup checks the gauge emission: one point per group, named
// and united per the descriptor, each carrying a non-empty metric.group attr.
func TestEmitSeriesByGroup(t *testing.T) {
	rec := telemetrytest.New()
	emitSeriesByGroup(rec.Emitter(), map[string]int{"Devices": 6, "other": 2})

	pts := rec.MetricPoints(appcatalog.MetricSeriesByGroup)
	if len(pts) != 2 {
		t.Fatalf("emitted %d series.by_group points, want 2", len(pts))
	}
	for _, p := range pts {
		if p.Unit != appcatalog.DocSeriesByGroup.Unit {
			t.Errorf("unit = %q, want %q", p.Unit, appcatalog.DocSeriesByGroup.Unit)
		}
		if p.Attrs[semconv.AttrMetricGroup] == "" {
			t.Errorf("point missing %s attr: %v", semconv.AttrMetricGroup, p.Attrs)
		}
	}
}
