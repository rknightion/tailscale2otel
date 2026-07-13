package nodemetrics

import (
	"fmt"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
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

	// Attempt to insert a brand-new series via the shared delta pipeline — it must
	// be rejected because the map is already at the hard cap (and reported as a
	// first observation, ok=false, so nothing is emitted).
	if d, ok := c.delta("test_total", 5, telemetry.Attrs{"k": "new"}); ok || d != 0 {
		t.Errorf("delta at cap = (%v, %v), want (0, false) — new series must not be tracked", d, ok)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.prev) != prevHardCap {
		t.Errorf("prev grew past cap: got %d entries, want %d (hard cap)", len(c.prev), prevHardCap)
	}
}
