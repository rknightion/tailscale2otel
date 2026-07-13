package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// pointForSet returns the value of the single point for metric name carrying the
// given dedup.set attribute, or fails if absent.
func pointForSet(t *testing.T, rec *telemetrytest.Recorder, name, set string) float64 {
	t.Helper()
	for _, p := range rec.MetricPoints(name) {
		if p.Attrs["dedup.set"] == set {
			return p.Value
		}
	}
	t.Fatalf("%s: no point for dedup.set=%q (points: %+v)", name, set, rec.MetricPoints(name))
	return 0
}

func hasPointForSet(rec *telemetrytest.Recorder, name, set string) bool {
	for _, p := range rec.MetricPoints(name) {
		if p.Attrs["dedup.set"] == set {
			return true
		}
	}
	return false
}

func TestRunDedupReporter_EmitsSizeAndEvictionDeltas(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		flow := dedup.New(2)
		for _, k := range []string{"a", "b", "c", "d"} { // 4 uniques into cap 2 => size 2, evictions 2
			flow.Add(k)
		}
		audit := dedup.New(10)
		audit.Add("x") // size 1, evictions 0

		sets := map[string]*dedup.Set{"flow": flow, "audit": audit, "webhook_cross": nil}
		go runDedupReporter(ctx, rec.Emitter(), time.Minute, sets)

		// First tick (immediate): sizes and the seeded eviction counters.
		synctest.Wait()
		if v := pointForSet(t, rec, "tailscale2otel.dedup.size", "flow"); v != 2 {
			t.Errorf("flow size = %v, want 2", v)
		}
		if v := pointForSet(t, rec, "tailscale2otel.dedup.size", "audit"); v != 1 {
			t.Errorf("audit size = %v, want 1", v)
		}
		if v := pointForSet(t, rec, "tailscale2otel.dedup.evictions", "flow"); v != 2 {
			t.Errorf("flow evictions = %v, want 2 (seed)", v)
		}
		// A nil set must be skipped entirely.
		if hasPointForSet(rec, "tailscale2otel.dedup.size", "webhook_cross") {
			t.Error("nil dedup set should not emit a size point")
		}

		// Drive two more evictions on flow, advance one interval: the cumulative
		// eviction counter reaches 4 and the size stays at the cap.
		flow.Add("e")
		flow.Add("f")
		time.Sleep(time.Minute)
		synctest.Wait()
		if v := pointForSet(t, rec, "tailscale2otel.dedup.evictions", "flow"); v != 4 {
			t.Errorf("flow evictions = %v, want 4 (cumulative)", v)
		}
		if v := pointForSet(t, rec, "tailscale2otel.dedup.size", "flow"); v != 2 {
			t.Errorf("flow size = %v, want 2 (bounded)", v)
		}
	})
}
