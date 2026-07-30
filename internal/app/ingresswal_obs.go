package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

func emitIngressWALHealth(e telemetry.Emitter, coordinator *ingressWALCoordinator) {
	if coordinator == nil || coordinator.wal == nil {
		return
	}
	health := coordinator.Health().WAL
	e.Gauge(
		appcatalog.DocIngressWALPendingEntries.Name,
		appcatalog.DocIngressWALPendingEntries.Unit,
		appcatalog.DocIngressWALPendingEntries.Description,
		float64(health.PendingEntries),
		nil,
	)
	e.Gauge(
		appcatalog.DocIngressWALPendingSize.Name,
		appcatalog.DocIngressWALPendingSize.Unit,
		appcatalog.DocIngressWALPendingSize.Description,
		float64(health.PendingBytes),
		nil,
	)
	e.Gauge(
		appcatalog.DocIngressWALOrphanStages.Name,
		appcatalog.DocIngressWALOrphanStages.Unit,
		appcatalog.DocIngressWALOrphanStages.Description,
		float64(health.OrphanStages),
		nil,
	)
	e.Gauge(
		appcatalog.DocIngressWALOrphanSize.Name,
		appcatalog.DocIngressWALOrphanSize.Unit,
		appcatalog.DocIngressWALOrphanSize.Description,
		float64(health.OrphanBytes),
		nil,
	)
	e.Gauge(
		appcatalog.DocIngressWALCompletionMarkers.Name,
		appcatalog.DocIngressWALCompletionMarkers.Unit,
		appcatalog.DocIngressWALCompletionMarkers.Description,
		float64(health.CompletionMarkers),
		nil,
	)
}

func runIngressWALReporter(
	ctx context.Context,
	e telemetry.Emitter,
	coordinator *ingressWALCoordinator,
	interval time.Duration,
) {
	if coordinator == nil || coordinator.wal == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	emitIngressWALHealth(e, coordinator)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emitIngressWALHealth(e, coordinator)
		}
	}
}
