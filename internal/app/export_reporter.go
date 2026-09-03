package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// emitExportDelta emits the increase in export volume since *last and advances
// *last. Counters are cumulative, so we feed the SDK the per-tick delta.
func emitExportDelta(e telemetry.Emitter, cur telemetry.ExportStats, last *telemetry.ExportStats) {
	dp := float64(cur.Datapoints - last.Datapoints)
	lr := float64(cur.LogRecords - last.LogRecords)
	telemetry.EmitExportStats(e, dp, lr)
	// Spans are a separate call rather than a third EmitExportStats parameter so
	// the existing signature stayed stable while #359 landed; the effect is the
	// same per-interval delta as the other two. Zero when tracing is off.
	telemetry.EmitExportSpanDelta(e, float64(cur.Spans-last.Spans))
	*last = cur
}

// runExportReporter emits tailscale2otel.export.{datapoints,log_records} once per
// export interval from the cumulative ExportStats snapshot. A non-positive
// interval falls back to 60s (matching the sibling reporters). stats returns the
// current cumulative counts (Provider.ExportStats).
func runExportReporter(ctx context.Context, e telemetry.Emitter, stats func() telemetry.ExportStats, interval time.Duration) {
	if stats == nil {
		return
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}
	var last telemetry.ExportStats
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emitExportDelta(e, stats(), &last)
		}
	}
}
