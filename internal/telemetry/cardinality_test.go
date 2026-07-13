package telemetry_test

import (
	"fmt"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
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

// TestCardinalityTracker_ConfigurableCap asserts a tracker built with an explicit
// cap pins each source metric's reported distinct-series count at that cap once
// exceeded (rather than the package default), so series.active faithfully signals
// when a metric is at the configured OTLP cardinality limit.
func TestCardinalityTracker_ConfigurableCap(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTrackerWithCap(3)
	for s := 0; s < 5; s++ {
		tr.Observe("tailscale.metric", telemetry.Attrs{"n": s})
	}
	tr.Report(rec.Emitter())

	if got := seriesActivePointsByName(t, rec)["tailscale.metric"]; got != 3 {
		t.Fatalf("series.active = %v, want 3 (pinned at the configured cap)", got)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Count != 3 || !snap[0].Capped {
		t.Fatalf("Snapshot = %+v, want one entry Count=3 Capped=true", snap)
	}
}

// TestCardinalityTracker_NonPositiveCapFallsBackToDefault asserts a non-positive
// cap (the "unlimited OTLP limit" case) falls back to the package memory-guard
// default rather than tracking unboundedly.
func TestCardinalityTracker_NonPositiveCapFallsBackToDefault(t *testing.T) {
	tr := telemetry.NewCardinalityTrackerWithCap(0)
	rec := telemetrytest.New()
	tr.Observe("tailscale.metric", telemetry.Attrs{"n": 1})
	tr.Report(rec.Emitter())
	if got := seriesActivePointsByName(t, rec)["tailscale.metric"]; got != 1 {
		t.Fatalf("series.active = %v, want 1 (default cap still tracks normally)", got)
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

// TestCardinalityTracker_SnapshotEmptyBeforeReport asserts Snapshot returns nil
// before the first Report — even after Observe, since Snapshot reflects the last
// COMPLETED export interval, not the in-progress one.
func TestCardinalityTracker_SnapshotEmptyBeforeReport(t *testing.T) {
	tr := telemetry.NewCardinalityTracker()
	tr.Observe("tailscale.metric", telemetry.Attrs{"a": "x"})
	if s := tr.Snapshot(); s != nil {
		t.Fatalf("Snapshot before first Report = %+v, want nil", s)
	}
}

// TestCardinalityTracker_SnapshotReflectsLastReport asserts Snapshot returns the
// per-source-metric counts from the most recent Report, sorted by count desc then
// name, mirroring the values emitted on the series.active gauge.
func TestCardinalityTracker_SnapshotReflectsLastReport(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()

	// "big" gets 3 distinct series, "small" gets 1 → expect big sorted first.
	tr.Observe("tailscale.small", telemetry.Attrs{"a": "x"})
	for i := 0; i < 3; i++ {
		tr.Observe("tailscale.big", telemetry.Attrs{"n": i})
	}
	tr.Report(rec.Emitter())

	got := tr.Snapshot()
	want := []telemetry.SeriesCount{
		{Metric: "tailscale.big", Count: 3, Capped: false},
		{Metric: "tailscale.small", Count: 1, Capped: false},
	}
	if len(got) != len(want) {
		t.Fatalf("Snapshot len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Snapshot[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestCardinalityTracker_SnapshotTracksReset asserts Snapshot follows the same
// per-interval reset semantics as the emitted gauge: after an empty interval it
// reflects that empty interval.
func TestCardinalityTracker_SnapshotTracksReset(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()

	tr.Observe("tailscale.metric", telemetry.Attrs{"a": "x"})
	tr.Report(rec.Emitter())
	if n := len(tr.Snapshot()); n != 1 {
		t.Fatalf("Snapshot after first report = %d entries, want 1", n)
	}

	tr.Report(rec.Emitter()) // empty interval
	if s := tr.Snapshot(); len(s) != 0 {
		t.Fatalf("Snapshot after empty interval = %+v, want empty", s)
	}
}

// TestCardinalityTracker_SnapshotCappedFlag asserts an over-cap metric is
// surfaced with Capped=true and the count pinned at the cap.
func TestCardinalityTracker_SnapshotCappedFlag(t *testing.T) {
	const cap = 10000 // defaultSeriesCap
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()

	for i := 0; i < cap+500; i++ {
		tr.Observe("tailscale.metric", telemetry.Attrs{"n": i})
	}
	tr.Report(rec.Emitter())

	got := tr.Snapshot()
	if len(got) != 1 {
		t.Fatalf("Snapshot len = %d, want 1: %+v", len(got), got)
	}
	if got[0].Count != cap || !got[0].Capped {
		t.Fatalf("Snapshot[0] = %+v, want Count=%d Capped=true", got[0], cap)
	}
}

// TestCardinalityTracker_SnapshotNilSafe asserts Snapshot is a no-op (nil, no
// panic) on a nil *CardinalityTracker.
func TestCardinalityTracker_SnapshotNilSafe(t *testing.T) {
	var tr *telemetry.CardinalityTracker
	if s := tr.Snapshot(); s != nil {
		t.Fatalf("nil tracker Snapshot = %+v, want nil", s)
	}
}

// seriesPointsByName indexes points of an arbitrary self-metric by metric.name.
func seriesPointsByName(t *testing.T, rec *telemetrytest.Recorder, metric string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, p := range rec.MetricPoints(metric) {
		out[p.Attrs[semconv.AttrMetricName]] = p.Value
	}
	return out
}

// TestCardinalityTracker_EmitsLimitAndOverflowing asserts that, with a positive
// configured limit, Report emits a single global series.limit equal to the limit
// and a per-metric series.overflowing that is 1 for capped metrics and 0 otherwise.
func TestCardinalityTracker_EmitsLimitAndOverflowing(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTrackerWithCap(3)

	// "hot" exceeds the cap of 3 -> capped; "cool" stays under.
	for s := 0; s < 5; s++ {
		tr.Observe("tailscale.hot", telemetry.Attrs{"n": s})
	}
	tr.Observe("tailscale.cool", telemetry.Attrs{"n": 0})
	tr.Report(rec.Emitter())

	// series.limit: exactly one global point = 3.
	limitPts := rec.MetricPoints("tailscale2otel.series.limit")
	if len(limitPts) != 1 {
		t.Fatalf("series.limit points = %d, want 1: %+v", len(limitPts), limitPts)
	}
	if limitPts[0].Value != 3 {
		t.Fatalf("series.limit = %v, want 3", limitPts[0].Value)
	}
	if name := limitPts[0].Attrs[semconv.AttrMetricName]; name != "" {
		t.Fatalf("series.limit carried metric.name=%q, want none (global)", name)
	}

	// series.overflowing: hot=1, cool=0.
	over := seriesPointsByName(t, rec, "tailscale2otel.series.overflowing")
	if over["tailscale.hot"] != 1 {
		t.Errorf("overflowing{hot} = %v, want 1", over["tailscale.hot"])
	}
	if over["tailscale.cool"] != 0 {
		t.Errorf("overflowing{cool} = %v, want 0", over["tailscale.cool"])
	}
}

// TestCardinalityTracker_UnlimitedSuppressesLimitAndOverflowing asserts that with
// no positive limit configured (the "unlimited OTLP limit" case), Report emits no
// series.limit and overflowing stays 0 — the tracker's memory-guard cap must NOT
// be reported as a real overflow.
func TestCardinalityTracker_UnlimitedSuppressesLimitAndOverflowing(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTrackerWithCap(0) // unlimited; falls back to memory guard internally

	tr.Observe("tailscale.metric", telemetry.Attrs{"n": 0})
	tr.Report(rec.Emitter())

	if n := len(rec.MetricPoints("tailscale2otel.series.limit")); n != 0 {
		t.Fatalf("series.limit points = %d, want 0 (no positive limit configured)", n)
	}
	over := seriesPointsByName(t, rec, "tailscale2otel.series.overflowing")
	if over["tailscale.metric"] != 0 {
		t.Fatalf("overflowing = %v, want 0 in unlimited mode", over["tailscale.metric"])
	}
}

// TestCardinalityTracker_SelfExclusionWholeFamily asserts the tracker never
// tracks ANY of its own tailscale2otel.series.* self-metrics — guarding the
// Report->Gauge->Observe recursion break for series.limit and series.overflowing
// as well as series.active.
func TestCardinalityTracker_SelfExclusionWholeFamily(t *testing.T) {
	rec := telemetrytest.New()
	tr := telemetry.NewCardinalityTracker()

	tr.Observe("tailscale2otel.series.active", telemetry.Attrs{semconv.AttrMetricName: "x"})
	tr.Observe("tailscale2otel.series.limit", telemetry.Attrs{})
	tr.Observe("tailscale2otel.series.overflowing", telemetry.Attrs{semconv.AttrMetricName: "x"})
	tr.Report(rec.Emitter())

	if s := tr.Snapshot(); len(s) != 0 {
		t.Fatalf("self-observed series.* family produced %+v, want no tracked series", s)
	}
}
