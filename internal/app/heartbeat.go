package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// runHeartbeat emits tailscale2otel.up=1 immediately and then on each interval
// until ctx is canceled, so the exporter's liveness is observable even when no
// collector has produced data yet. A non-positive interval falls back to 60s
// (time.NewTicker(0) panics).
//
// extra are additional emitters run on the same schedule. The capability matrix
// (#430) rides here rather than on a collector tick because it must be
// observable even when every collector is disabled or failing — that is exactly
// the state it exists to explain.
func runHeartbeat(ctx context.Context, e telemetry.Emitter, interval time.Duration, extra ...func(telemetry.Emitter)) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	emit := func() {
		e.Gauge(appcatalog.DocUp.Name, appcatalog.DocUp.Unit, appcatalog.DocUp.Description, 1, nil)
		for _, fn := range extra {
			if fn != nil {
				fn(e)
			}
		}
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
