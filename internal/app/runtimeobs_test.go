package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// onePoint returns the single recorded point for name, failing if there isn't
// exactly one (these runtime metrics carry no attributes, so one series each).
func onePoint(t *testing.T, rec *telemetrytest.Recorder, name string) telemetrytest.MetricPoint {
	t.Helper()
	pts := rec.MetricPoints(name)
	if len(pts) != 1 {
		t.Fatalf("%s: got %d points, want 1 (%+v)", name, len(pts), pts)
	}
	return pts[0]
}

func TestRunRuntimeReporter_EmitsGaugesAndCounterDeltas(t *testing.T) {
	stats1 := runtimeStats{
		goroutines: 10, gomaxprocs: 8,
		heapAlloc: 1000, heapSys: 2000, heapInuse: 1500, stackInuse: 300, sys: 5000,
		heapObjects: 42, nextGC: 4096, gcCPUFraction: 0.01,
		numGC: 3, pauseTotalNs: 2_000_000_000, totalAlloc: 10000,
	}
	stats2 := runtimeStats{
		goroutines: 12, gomaxprocs: 8,
		heapAlloc: 1200, heapSys: 2200, heapInuse: 1600, stackInuse: 320, sys: 5200,
		heapObjects: 50, nextGC: 8192, gcCPUFraction: 0.02,
		numGC: 5, pauseTotalNs: 3_000_000_000, totalAlloc: 15000,
	}

	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		calls := 0
		read := func() runtimeStats {
			calls++
			if calls == 1 {
				return stats1
			}
			return stats2
		}

		go runRuntimeReporter(ctx, rec.Emitter(), time.Minute, read)

		// First tick (emitted immediately on start): gauges reflect stats1; the
		// cumulative counters are seeded with the full since-start values.
		synctest.Wait()
		if v := onePoint(t, rec, "tailscale2otel.runtime.goroutines").Value; v != 10 {
			t.Errorf("goroutines = %v, want 10", v)
		}
		if v := onePoint(t, rec, "tailscale2otel.runtime.memory.heap_alloc").Value; v != 1000 {
			t.Errorf("heap_alloc = %v, want 1000", v)
		}
		if v := onePoint(t, rec, "tailscale2otel.runtime.gc.next_target").Value; v != 4096 {
			t.Errorf("gc.next_target = %v, want 4096", v)
		}
		if v := onePoint(t, rec, "tailscale2otel.runtime.gc.count").Value; v != 3 {
			t.Errorf("gc.count = %v, want 3 (seed)", v)
		}
		if v := onePoint(t, rec, "tailscale2otel.runtime.gc.pause_time").Value; v != 2 {
			t.Errorf("gc.pause_time = %v, want 2.0 (seed)", v)
		}
		if v := onePoint(t, rec, "tailscale2otel.runtime.memory.alloc").Value; v != 10000 {
			t.Errorf("alloc = %v, want 10000 (seed)", v)
		}

		// The counters must be monotonic sums, the memory gauges must be gauges.
		if p := onePoint(t, rec, "tailscale2otel.runtime.gc.count"); p.Kind != "sum" || !p.Monotonic {
			t.Errorf("gc.count kind=%q monotonic=%v, want sum/true", p.Kind, p.Monotonic)
		}
		if p := onePoint(t, rec, "tailscale2otel.runtime.memory.heap_alloc"); p.Kind != "gauge" {
			t.Errorf("heap_alloc kind=%q, want gauge", p.Kind)
		}

		// Second tick: gauges move to stats2; counters reach stats2's cumulative
		// (seed delta + second delta), proving delta-accumulation under cumulative
		// temporality.
		time.Sleep(time.Minute)
		synctest.Wait()
		if v := onePoint(t, rec, "tailscale2otel.runtime.goroutines").Value; v != 12 {
			t.Errorf("goroutines = %v, want 12", v)
		}
		if v := onePoint(t, rec, "tailscale2otel.runtime.gc.count").Value; v != 5 {
			t.Errorf("gc.count = %v, want 5 (cumulative)", v)
		}
		if v := onePoint(t, rec, "tailscale2otel.runtime.gc.pause_time").Value; v != 3 {
			t.Errorf("gc.pause_time = %v, want 3.0 (cumulative)", v)
		}
		if v := onePoint(t, rec, "tailscale2otel.runtime.memory.alloc").Value; v != 15000 {
			t.Errorf("alloc = %v, want 15000 (cumulative)", v)
		}
	})
}

func TestEmitRuntime_CounterResetGuardSkipsNegativeDelta(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()
	var last runtimeStats

	// Tick 1: seed gc.count to 10.
	emitRuntime(e, runtimeStats{numGC: 10}, &last)
	if v := onePoint(t, rec, "tailscale2otel.runtime.gc.count").Value; v != 10 {
		t.Fatalf("after seed: gc.count = %v, want 10", v)
	}

	// Tick 2: a smaller value (a wrap/reset) must NOT emit a huge wrapped delta;
	// the counter stays flat and last is updated to the new baseline.
	emitRuntime(e, runtimeStats{numGC: 4}, &last)
	if v := onePoint(t, rec, "tailscale2otel.runtime.gc.count").Value; v != 10 {
		t.Fatalf("after reset: gc.count = %v, want 10 (delta skipped)", v)
	}

	// Tick 3: growth from the new baseline (4 -> 7) adds 3 -> 13.
	emitRuntime(e, runtimeStats{numGC: 7}, &last)
	if v := onePoint(t, rec, "tailscale2otel.runtime.gc.count").Value; v != 13 {
		t.Fatalf("after growth: gc.count = %v, want 13", v)
	}
}
