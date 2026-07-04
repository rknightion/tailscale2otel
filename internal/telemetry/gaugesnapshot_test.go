package telemetry_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestGaugeSnapshotBuilder_FlushesAndClears exercises the builder's core
// contract across ticks: it emits every gauge it has ever seen each Flush, and
// a gauge (or a single series) that produces no points in a later tick is
// cleared rather than ghosting (#55).
func TestGaugeSnapshotBuilder_FlushesAndClears(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	const online = "tailscale.device.online"
	const skew = "tailscale.device.version_skew"

	// Tick 1: two devices for `online`, one of them also reports `skew`.
	b := telemetry.NewGaugeSnapshotBuilder()
	b.Add(online, "1", "online flag", 1, telemetry.Attrs{"id": "a"})
	b.Add(online, "1", "online flag", 1, telemetry.Attrs{"id": "b"})
	b.Add(skew, "1", "minors behind", 3, telemetry.Attrs{"id": "b"})
	b.Flush(e)

	if got := idsOf(rec.MetricPoints(online)); len(got) != 2 || got["a"] != 1 || got["b"] != 1 {
		t.Fatalf("tick 1 online: got %v, want a=1,b=1", got)
	}
	if got := idsOf(rec.MetricPoints(skew)); len(got) != 1 || got["b"] != 3 {
		t.Fatalf("tick 1 skew: got %v, want b=3", got)
	}

	// Tick 2 on the SAME builder: device b is gone entirely. `online` now only
	// has a; `skew` gets no Add at all this tick.
	b.Add(online, "1", "online flag", 1, telemetry.Attrs{"id": "a"})
	b.Flush(e)

	got := idsOf(rec.MetricPoints(online))
	if len(got) != 1 || got["a"] != 1 {
		t.Fatalf("tick 2 online: got %v, want only a=1 (b must drop out)", got)
	}
	if _, ok := got["b"]; ok {
		t.Errorf("tick 2 online: departed device b still present (ghost): %v", got)
	}
	// skew got no points this tick, but the builder still flushes it empty —
	// its prior series (b) must be cleared, not left ghosting.
	if pts := rec.MetricPoints(skew); len(pts) != 0 {
		t.Errorf("tick 2 skew: got %d series, want 0 (an un-Added managed gauge must clear)", len(pts))
	}
}
