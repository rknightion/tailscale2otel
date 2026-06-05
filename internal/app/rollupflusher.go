package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// runRollupFlusher drains the flow processor's rollup accumulator once per export
// interval, emitting the bounded *.rollup counters and the per-source-node unique
// gauges. It is started only when cardinality.flow.metrics_mode is rollup or both
// (FlushRollup is a no-op otherwise). A non-positive interval falls back to 60s,
// mirroring runCardinalityReporter and the telemetry provider
// (time.NewTicker(0) panics). On ctx cancellation it returns without flushing; the
// app performs one authoritative final flush before shutting the pipeline down so
// the last interval's accumulated counts are not lost.
func runRollupFlusher(ctx context.Context, proc *flowlog.Processor, e telemetry.Emitter, interval time.Duration) {
	if proc == nil {
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
			proc.FlushRollup(e)
		}
	}
}
