package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

func TestFreshnessState(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := time.Minute
	cases := []struct {
		name      string
		last      time.Time
		wantState string
	}{
		{"never", time.Time{}, "none"},
		{"fresh", now.Add(-30 * time.Second), "ok"},             // < 2 intervals
		{"warning", now.Add(-3 * time.Minute), "warning"},       // between 2 and 5
		{"stale", now.Add(-10 * time.Minute), "stale"},          // > 5 intervals
		{"boundary_ok", now.Add(-2 * time.Minute), "ok"},        // exactly 2 intervals
		{"boundary_warn", now.Add(-5 * time.Minute), "warning"}, // exactly 5 intervals
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, state := freshnessState(c.last, now, interval)
			if state != c.wantState {
				t.Errorf("state = %q, want %q", state, c.wantState)
			}
		})
	}
	// Non-positive interval degrades to ok for any success.
	if _, _, state := freshnessState(now.Add(-time.Hour), now, 0); state != "ok" {
		t.Errorf("zero interval: state = %q, want ok", state)
	}
}

func TestCardSeriesLevel(t *testing.T) {
	th := statusdata.CardinalityThresholds{Warning: 2000, Critical: 8000}
	cases := []struct {
		count int
		want  string
	}{{100, ""}, {2000, "warning"}, {7999, "warning"}, {8000, "critical"}, {9000, "critical"}}
	for _, c := range cases {
		if got := cardSeriesLevel(c.count, th); got != c.want {
			t.Errorf("level(%d) = %q, want %q", c.count, got, c.want)
		}
	}
	// Disabled levels (0) never fire.
	if got := cardSeriesLevel(1_000_000, statusdata.CardinalityThresholds{}); got != "" {
		t.Errorf("disabled thresholds: level = %q, want empty", got)
	}
}

func TestGrowthDeltaPct(t *testing.T) {
	cases := []struct {
		series []int
		want   float64
	}{
		{[]int{100, 150}, 50},
		{[]int{200, 100}, -50},
		{[]int{100}, 0},      // < 2 samples
		{nil, 0},             // empty
		{[]int{0, 50}, 0},    // started at zero -> guarded
		{[]int{100, 100}, 0}, // flat
	}
	for _, c := range cases {
		if got := growthDeltaPct(c.series); got != c.want {
			t.Errorf("growthDeltaPct(%v) = %v, want %v", c.series, got, c.want)
		}
	}
}

func TestBuildLabelRows_AggregatesAcrossMetrics(t *testing.T) {
	labels := []telemetry.LabelStat{
		{Metric: "m1", Label: "user", Distinct: 5, Examples: []string{"a", "b"}},
		{Metric: "m2", Label: "user", Distinct: 3, Capped: true, Examples: []string{"c"}},
		{Metric: "m1", Label: "os", Distinct: 2},
	}
	rows := buildLabelRows(labels, map[string]metricdoc.Metric{})
	if len(rows) != 2 {
		t.Fatalf("want 2 label rows, got %d", len(rows))
	}
	// "user" (5+3=8) sorts before "os" (2).
	if rows[0].Label != "user" || rows[0].TotalDistinct != 8 {
		t.Errorf("row0 = %+v, want user/8", rows[0])
	}
	if !rows[0].Capped {
		t.Error("user row should be capped (m2 contributed capped)")
	}
	// Within user, m1 (5) before m2 (3).
	if rows[0].Metrics[0].Metric != "m1" {
		t.Errorf("user metrics order = %v, want m1 first", rows[0].Metrics)
	}
}

func TestBuildGrowthRows_SortedByAbsDelta(t *testing.T) {
	per := map[string][]int{
		"slow": {100, 110}, // +10%
		"fast": {100, 200}, // +100%
		"drop": {200, 100}, // -50%
	}
	rows := buildGrowthRows(per, map[string]metricdoc.Metric{})
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].Metric != "fast" {
		t.Errorf("biggest mover = %q, want fast", rows[0].Metric)
	}
	if rows[0].Current != 200 {
		t.Errorf("fast current = %d, want 200", rows[0].Current)
	}
}

func TestCardinalityInfo_ThresholdsAlertsAndGating(t *testing.T) {
	th := statusdata.CardinalityThresholds{Warning: 100, Critical: 500}
	series := []telemetry.SeriesCount{
		{Metric: "big", Count: 600},
		{Metric: "mid", Count: 150},
		{Metric: "small", Count: 10},
	}
	mbn := map[string]metricdoc.Metric{}

	// self-obs off -> unavailable, but thresholds still carried.
	off := cardinalityInfo(false, series, nil, nil, th, mbn)
	if off.Available {
		t.Fatal("self-obs off should be Available=false")
	}
	if off.Thresholds != th {
		t.Errorf("thresholds should carry even when unavailable, got %+v", off.Thresholds)
	}

	info := cardinalityInfo(true, series, nil, nil, th, mbn)
	if !info.Available || info.TotalMetrics != 3 || info.Total != 760 {
		t.Fatalf("summary = avail %v metrics %d total %d", info.Available, info.TotalMetrics, info.Total)
	}
	levels := map[string]string{}
	for _, s := range info.Series {
		levels[s.Metric] = s.Level
	}
	if levels["big"] != "critical" || levels["mid"] != "warning" || levels["small"] != "" {
		t.Errorf("levels = %v", levels)
	}
	// Alerts: 2 (big, mid), critical first.
	if len(info.Alerts) != 2 || info.Alerts[0].Metric != "big" || info.Alerts[0].Level != "critical" {
		t.Errorf("alerts = %+v", info.Alerts)
	}
}
