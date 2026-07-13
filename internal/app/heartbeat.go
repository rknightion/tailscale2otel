package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

// runHeartbeat emits tailscale2otel.up=1 immediately and then on each interval
// until ctx is canceled, so the exporter's liveness is observable even when no
// collector has produced data yet. A non-positive interval falls back to 60s
// (time.NewTicker(0) panics).
func runHeartbeat(ctx context.Context, e telemetry.Emitter, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	emit := func() {
		e.Gauge(appcatalog.DocUp.Name, appcatalog.DocUp.Unit, appcatalog.DocUp.Description, 1, nil)
	}
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
