package telemetry_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// idsOf returns the set of the "id" attribute across the recorded points for a
// metric, for order-independent series-presence assertions.
func idsOf(pts []telemetrytest.MetricPoint) map[string]float64 {
	out := make(map[string]float64, len(pts))
	for _, p := range pts {
		out[p.Attrs["id"]] = p.Value
	}
	return out
}

// TestGaugeSnapshot_DepartedSeriesDropsOut is the proof that GaugeSnapshot fixes
// the ghost-series problem (#55): a series present in one snapshot but absent
// from the next disappears from the export on the next collection, rather than
// lingering at its last value forever the way a synchronous cumulative Gauge
// would. The Recorder uses a real ManualReader with the default cumulative
// temporality, so this exercises the SDK's actual observable/precomputed
// aggregation — the same path production uses.
func TestGaugeSnapshot_DepartedSeriesDropsOut(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	const name = "tailscale.device.online"

	// Cycle 1: two devices online.
	e.GaugeSnapshot(name, "1", "device online flag", []telemetry.GaugePoint{
		{Value: 1, Attrs: telemetry.Attrs{"id": "aaaa"}},
		{Value: 1, Attrs: telemetry.Attrs{"id": "bbbb"}},
	})
	got := idsOf(rec.MetricPoints(name))
	if len(got) != 2 || got["aaaa"] != 1 || got["bbbb"] != 1 {
		t.Fatalf("cycle 1: got %v, want both aaaa=1 and bbbb=1", got)
	}

	// Cycle 2: device bbbb has left the tailnet — snapshot now holds only aaaa.
	e.GaugeSnapshot(name, "1", "device online flag", []telemetry.GaugePoint{
		{Value: 1, Attrs: telemetry.Attrs{"id": "aaaa"}},
	})
	got = idsOf(rec.MetricPoints(name))
	if len(got) != 1 {
		t.Fatalf("cycle 2: got %d series %v, want exactly 1 (bbbb must drop out, not ghost)", len(got), got)
	}
	if _, ok := got["bbbb"]; ok {
		t.Errorf("cycle 2: departed series bbbb is still exported (ghost) — got %v", got)
	}
	if got["aaaa"] != 1 {
		t.Errorf("cycle 2: surviving series aaaa = %v, want 1", got["aaaa"])
	}
}

// TestGaugeSnapshot_EmptyClears verifies that an empty snapshot clears every
// series (an entire collector's entities going away → nothing exported).
func TestGaugeSnapshot_EmptyClears(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()
	const name = "tailscale.node.up"

	e.GaugeSnapshot(name, "1", "node up", []telemetry.GaugePoint{
		{Value: 1, Attrs: telemetry.Attrs{"id": "n1"}},
	})
	if pts := rec.MetricPoints(name); len(pts) != 1 {
		t.Fatalf("after first snapshot: %d points, want 1", len(pts))
	}
	e.GaugeSnapshot(name, "1", "node up", nil)
	if pts := rec.MetricPoints(name); len(pts) != 0 {
		t.Fatalf("after empty snapshot: %d points, want 0 (all series cleared)", len(pts))
	}
}
