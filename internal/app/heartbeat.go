package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// runHeartbeat emits tailscale2otel.up=1 immediately and then on each interval
// until ctx is cancelled, so the exporter's liveness is observable even when no
// collector has produced data yet.
func runHeartbeat(ctx context.Context, e telemetry.Emitter, interval time.Duration) {
	emit := func() { e.Gauge("tailscale2otel.up", "1", "exporter is running (1)", 1, nil) }
	emit()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emit()
		}
	}
}
