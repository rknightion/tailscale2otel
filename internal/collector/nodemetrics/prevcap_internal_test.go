package nodemetrics

import (
	"fmt"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestEmitDeltaBaselineMapIsCapped verifies that prevHardCap bounds the
// delta-baseline map: label-churning targets (or a malicious node minting
// unique label values) must not grow c.prev without bound between
// pruneStale generations.
func TestEmitDeltaBaselineMapIsCapped(t *testing.T) {
	c := New(Options{})

	// Fill to the cap with synthetic baselines (raw key insertion, bypassing
	// seriesKey to keep the test fast and focused on the cap enforcement).
	c.mu.Lock()
	for i := 0; i < prevHardCap; i++ {
		c.prev[fmt.Sprintf("m\x00k=%d", i)] = prevEntry{value: 1, gen: 1}
	}
	c.mu.Unlock()

	// Attempt to insert a brand-new series via emitDelta — it must be rejected
	// because the map is already at the hard cap.
	rec := telemetrytest.New()
	s := &sample{name: "test_total", value: 5, cumulative: true}
	c.emitDelta(s, telemetry.Attrs{"k": "new"}, rec.Emitter())

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.prev) != prevHardCap {
		t.Errorf("prev grew past cap: got %d entries, want %d (hard cap)", len(c.prev), prevHardCap)
	}
}
