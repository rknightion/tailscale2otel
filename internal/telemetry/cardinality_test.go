package telemetry_test

import (
	"fmt"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// pointsByMetricName indexes the series.active points emitted into rec by the
// value of their metric.name attribute, so a test can assert the per-source-
// metric distinct-series count.
func seriesActivePointsByName(t *testing.T, rec *telemetrytest.Recorder) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale2otel.series.active") {
		name := p.Attrs[semconv.AttrMetricName]
		if name == "" {
			t.Fatalf("series.active point missing %q attribute: %+v", semconv.AttrMetricName, p)
		}
		out[name] = p.Value
	}
	return out
}

// TestCardinalityTracker_ExactDistinctCountPerMetric drives M source metrics
// with N distinct fingerprints each and asserts Report emits exactly M points,
// one per source metric, each carrying the exact distinct count N.
func TestCardinalityTracker_ExactDistinctCountPerMetric(t *testing.T) {
	const (
		metricCount = 3
		seriesPer   = 5
	)
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()

	for m := 0; m < metricCount; m++ {
		name := fmt.Sprintf("tailscale.metric.%d", m)
		for s := 0; s < seriesPer; s++ {
			tr.Observe(name, telemetry.Attrs{"a": "x", "n": s})
		}
	}

	tr.Report(rec.Emitter())

	got := seriesActivePointsByName(t, rec)
	if len(got) != metricCount {
		t.Fatalf("got %d series.active points, want %d: %+v", len(got), metricCount, got)
	}
	for m := 0; m < metricCount; m++ {
		name := fmt.Sprintf("tailscale.metric.%d", m)
		if got[name] != float64(seriesPer) {
			t.Errorf("series.active{%s=%s} = %v, want %d", semconv.AttrMetricName, name, got[name], seriesPer)
		}
	}
}

// TestCardinalityTracker_DeterministicFingerprint asserts the same attribute
// set observed twice counts as ONE series, despite Go's randomized map order.
func TestCardinalityTracker_DeterministicFingerprint(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()

	// Many keys so a non-deterministic (map-order-dependent) fingerprint would
	// very likely collide-or-diverge across the two calls.
	a := telemetry.Attrs{"k1": "v1", "k2": "v2", "k3": int64(3), "k4": true, "k5": 5.0, "k6": []string{"a", "b"}}
	b := telemetry.Attrs{"k6": []string{"a", "b"}, "k5": 5.0, "k4": true, "k3": int64(3), "k2": "v2", "k1": "v1"}
	tr.Observe("tailscale.metric", a)
	tr.Observe("tailscale.metric", b)

	tr.Report(rec.Emitter())

	got := seriesActivePointsByName(t, rec)
	if got["tailscale.metric"] != 1 {
		t.Fatalf("identical attrs observed twice = %v distinct series, want 1", got["tailscale.metric"])
	}
}

// TestCardinalityTracker_ResetBetweenReports asserts the tracker measures
// active-per-interval: a second Report with no Observe in between emits no
// points (the set was reset).
func TestCardinalityTracker_ResetBetweenReports(t *testing.T) {
	tr := telemetry.NewCardinalityTracker()

	rec1 := telemetrytest.New()
	tr.Observe("tailscale.metric", telemetry.Attrs{"a": "x"})
	tr.Report(rec1.Emitter())
	if n := len(rec1.MetricPoints("tailscale2otel.series.active")); n != 1 {
		t.Fatalf("first report: got %d points, want 1", n)
	}

	rec2 := telemetrytest.New()
	tr.Report(rec2.Emitter())
	if n := len(rec2.MetricPoints("tailscale2otel.series.active")); n != 0 {
		t.Fatalf("second report (no Observe between): got %d points, want 0 (reset)", n)
	}
}

// TestCardinalityTracker_PinsAtCap asserts that inserting more than the
// per-metric cap pins the reported value at the cap rather than growing
// unbounded.
func TestCardinalityTracker_PinsAtCap(t *testing.T) {
	const cap = 10000 // defaultSeriesCap
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()

	for i := 0; i < cap+500; i++ {
		tr.Observe("tailscale.metric", telemetry.Attrs{"n": i})
	}

	tr.Report(rec.Emitter())
	got := seriesActivePointsByName(t, rec)
	if got["tailscale.metric"] != float64(cap) {
		t.Fatalf("over-cap series.active = %v, want pinned at cap %d", got["tailscale.metric"], cap)
	}
}

// TestCardinalityTracker_SelfExclusion asserts the tracker never tracks its own
// self-metric (which also guards against Report->Gauge->Observe recursion).
func TestCardinalityTracker_SelfExclusion(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()

	tr.Observe("tailscale2otel.series.active", telemetry.Attrs{semconv.AttrMetricName: "anything"})
	tr.Report(rec.Emitter())

	if n := len(rec.MetricPoints("tailscale2otel.series.active")); n != 0 {
		t.Fatalf("self-observed series.active produced %d points, want 0 (self-exclusion)", n)
	}
}

// TestCardinalityTracker_NilSafe asserts Observe and Report are no-ops (no
// panic, no emission) on a nil *CardinalityTracker.
func TestCardinalityTracker_NilSafe(t *testing.T) {
	var tr *telemetry.CardinalityTracker
	rec := telemetrytest.New()

	// Must not panic.
	tr.Observe("tailscale.metric", telemetry.Attrs{"a": "x"})
	tr.Report(rec.Emitter())

	if n := len(rec.MetricPoints("tailscale2otel.series.active")); n != 0 {
		t.Fatalf("nil tracker emitted %d points, want 0", n)
	}
}
