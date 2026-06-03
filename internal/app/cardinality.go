package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// runCardinalityReporter reports the tailscale2otel.series.active gauge once per
// export interval. It does NOT emit on the first tick (a full interval must
// elapse to measure active-per-interval cardinality) and returns immediately
// when self-observability is disabled (c == nil).
func runCardinalityReporter(ctx context.Context, e telemetry.Emitter, c *telemetry.CardinalityTracker, interval time.Duration) {
	if c == nil {
		return
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
