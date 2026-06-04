package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// runCardinalityReporter reports the tailscale2otel.series.active gauge once per
// export interval. It does NOT emit on the first tick (a full interval must
// elapse to measure active-per-interval cardinality) and returns immediately
// when self-observability is disabled (c == nil). A non-positive interval falls
// back to 60s, mirroring the telemetry provider and the sibling reporters
// (time.NewTicker(0) panics).
func runCardinalityReporter(ctx context.Context, e telemetry.Emitter, c *telemetry.CardinalityTracker, interval time.Duration) {
	if c == nil {
		return
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Report(e)
		}
	}
}
