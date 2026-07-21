package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

func TestRuntimeHistory_SampleComputesGCRate(t *testing.T) {
	h := newRuntimeHistory(10)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// First sample: no prior, so GC rate is 0.
	h.sample(t0, samplerTick{rs: runtimeStats{goroutines: 5, heapAlloc: 1000, numGC: 10}, cardTotal: 100, perMetric: map[string]int{"m": 3}})
	if g := h.gcRate.Values(); len(g) != 1 || g[0] != 0 {
		t.Fatalf("first gcRate = %v, want [0]", g)
	}

	// 10s later, NumGC advanced by 5 -> 0.5 cycles/sec.
	h.sample(t0.Add(10*time.Second), samplerTick{rs: runtimeStats{goroutines: 6, heapAlloc: 2000, numGC: 15}, cardTotal: 110, perMetric: map[string]int{"m": 4}})
	if g := h.gcRate.Values(); len(g) != 2 || g[1] != 0.5 {
		t.Fatalf("gcRate = %v, want second 0.5", g)
	}
	if gr := h.goroutines.Values(); len(gr) != 2 || gr[0] != 5 || gr[1] != 6 {
		t.Fatalf("goroutines = %v, want [5 6]", gr)
	}
	if ha := h.heapAlloc.Values(); len(ha) != 2 || ha[0] != 1000 || ha[1] != 2000 {
		t.Fatalf("heapAlloc = %v, want [1000 2000]", ha)
	}
	if ct := h.cardTotal.Values(); len(ct) != 2 || ct[0] != 100 || ct[1] != 110 {
		t.Fatalf("cardTotal = %v, want [100 110]", ct)
	}
}

func TestRuntimeHistory_SampleHandlesNumGCWrap(t *testing.T) {
	h := newRuntimeHistory(10)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.sample(t0, samplerTick{rs: runtimeStats{numGC: 100}})
	h.sample(t0.Add(time.Second), samplerTick{rs: runtimeStats{numGC: 50}}) // counter went backwards
	if g := h.gcRate.Values(); len(g) != 2 || g[1] != 0 {
		t.Fatalf("gcRate after wrap = %v, want second 0 (no negative rate)", g)
	}
}

func TestRunSampler_SamplesOnStartAndTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newRuntimeHistory(60)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		read := func() runtimeStats { return runtimeStats{goroutines: 7} }
		card := func() int { return 42 }

		go runSampler(ctx, h, time.Hour, samplerSources{
			read:      read,
			cardTotal: card,
			perMetric: func() map[string]int { return map[string]int{"m": 9} },
		})

		synctest.Wait()
		if got := h.goroutines.Len(); got != 1 {
			t.Fatalf("after start samples = %d, want 1", got)
		}

		time.Sleep(time.Hour)
		synctest.Wait()
		if got := h.goroutines.Len(); got != 2 {
			t.Fatalf("after one tick samples = %d, want 2", got)
		}
		if ct := h.cardTotal.Values(); len(ct) == 0 || ct[len(ct)-1] != 42 {
			t.Fatalf("cardTotal last = %v, want 42", ct)
		}
	})
}

func TestRuntimeHistory_PerMetricSeries(t *testing.T) {
	h := newRuntimeHistory(3)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.sample(t0, samplerTick{cardTotal: 5, perMetric: map[string]int{"a": 3}})
	h.sample(t0.Add(time.Second), samplerTick{cardTotal: 7, perMetric: map[string]int{"a": 4, "b": 1}})

	got := h.perMetricSeries()
	if want := []int{3, 4}; !equalInts(got["a"], want) {
		t.Errorf("a series = %v, want %v", got["a"], want)
	}
	// b appeared only on the second tick -> its own fresh ring.
	if want := []int{1}; !equalInts(got["b"], want) {
		t.Errorf("b series = %v, want %v", got["b"], want)
	}
}

// TestRuntimeHistory_SampleComputesEmitRates covers the emit-boundary rate
// differencing: the first sample has no prior total (rate 0, never the whole
// cumulative count), a moved counter yields per-second deltas over the elapsed
// wall time, and a counter that did not move yields 0 rather than holding the
// previous rate.
func TestRuntimeHistory_SampleComputesEmitRates(t *testing.T) {
	h := newRuntimeHistory(10)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// First sample: process already emitted 500 points, but with no prior total
	// the rate must be 0 (not 500/s, and not 500).
	h.sample(t0, samplerTick{emit: telemetry.EmitStats{MetricPoints: 500, LogRecords: 50}})
	if m, l := h.metricRate.Values(), h.logRate.Values(); len(m) != 1 || m[0] != 0 || len(l) != 1 || l[0] != 0 {
		t.Fatalf("first sample rates = %v / %v, want [0] / [0]", m, l)
	}

	// 10s later: +100 metric points, +20 log records -> 10/s and 2/s.
	h.sample(t0.Add(10*time.Second), samplerTick{emit: telemetry.EmitStats{MetricPoints: 600, LogRecords: 70}})
	if m := h.metricRate.Values(); len(m) != 2 || m[1] != 10 {
		t.Fatalf("metricRate = %v, want second 10", m)
	}
	if l := h.logRate.Values(); len(l) != 2 || l[1] != 2 {
		t.Fatalf("logRate = %v, want second 2", l)
	}

	// 10s later still, nothing emitted: the rate drops to 0.
	h.sample(t0.Add(20*time.Second), samplerTick{emit: telemetry.EmitStats{MetricPoints: 600, LogRecords: 70}})
	if m := h.metricRate.Values(); len(m) != 3 || m[2] != 0 {
		t.Fatalf("metricRate for an idle interval = %v, want third 0", m)
	}
	if l := h.logRate.Values(); len(l) != 3 || l[2] != 0 {
		t.Fatalf("logRate for an idle interval = %v, want third 0", l)
	}
}

// TestRuntimeHistory_SampleRecordsFleet asserts the collector-fleet aggregate is
// appended to its own rings each tick.
func TestRuntimeHistory_SampleRecordsFleet(t *testing.T) {
	h := newRuntimeHistory(10)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.sample(t0, samplerTick{fleet: fleetStats{active: 4, failing: 1, meanDurationMs: 120}})
	h.sample(t0.Add(10*time.Second), samplerTick{fleet: fleetStats{active: 4, failing: 0, meanDurationMs: 80}})

	if got := h.failing.Values(); !equalInts(got, []int{1, 0}) {
		t.Errorf("failing series = %v, want [1 0]", got)
	}
	if got := h.runDurMs.Values(); len(got) != 2 || got[0] != 120 || got[1] != 80 {
		t.Errorf("run duration series = %v, want [120 80]", got)
	}
}

// TestFleetAggregate covers the fleet reduction across collector status
// snapshots: only collectors that have actually run count, a failed last run is
// counted as failing, and the mean duration averages the last run of every
// active collector across every tailnet runtime.
func TestFleetAggregate(t *testing.T) {
	tests := []struct {
		name  string
		snaps []map[string]collector.CollectorRun
		want  fleetStats
	}{
		{
			name: "no collectors",
			want: fleetStats{},
		},
		{
			name: "never-run collectors are not active",
			snaps: []map[string]collector.CollectorRun{{
				"devices": {Runs: 0},
			}},
			want: fleetStats{},
		},
		{
			name: "mixed outcomes",
			snaps: []map[string]collector.CollectorRun{{
				"devices":  {Runs: 3, LastSuccess: true, LastDuration: 100 * time.Millisecond},
				"flowlogs": {Runs: 2, LastSuccess: false, LastDuration: 300 * time.Millisecond},
			}},
			want: fleetStats{active: 2, failing: 1, meanDurationMs: 200},
		},
		{
			name: "aggregated across runtimes",
			snaps: []map[string]collector.CollectorRun{
				{"devices": {Runs: 1, LastSuccess: true, LastDuration: 50 * time.Millisecond}},
				{"devices": {Runs: 1, LastSuccess: false, LastDuration: 150 * time.Millisecond}},
			},
			want: fleetStats{active: 2, failing: 1, meanDurationMs: 100},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fleetAggregate(tc.snaps...); got != tc.want {
				t.Errorf("fleetAggregate = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
