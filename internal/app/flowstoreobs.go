package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

func emitFlowStoreObservability(rt *tailnetRuntime) {
	if rt == nil || rt.flowStore == nil || rt.emitter == nil {
		return
	}
	backend := rt.flowStore.Stats().Backend
	if backend.Kind != flowstore.BackendSQLite {
		return
	}
	rt.emitter.Gauge(appcatalog.DocFlowStoreJournalSize.Name, appcatalog.DocFlowStoreJournalSize.Unit,
		appcatalog.DocFlowStoreJournalSize.Description, float64(backend.JournalSizeBytes), nil)
	points := []telemetry.GaugePoint(nil)
	if !backend.LastCheckpointAt.IsZero() {
		points = append(points, telemetry.GaugePoint{Value: float64(backend.LastCheckpointAt.UnixNano()) / float64(time.Second)})
	}
	rt.emitter.GaugeSnapshot(appcatalog.DocFlowStoreLastCheckpointTimestamp.Name,
		appcatalog.DocFlowStoreLastCheckpointTimestamp.Unit,
		appcatalog.DocFlowStoreLastCheckpointTimestamp.Description, points)
}

func runFlowStoreReporter(ctx context.Context, rt *tailnetRuntime, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	emitFlowStoreObservability(rt)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emitFlowStoreObservability(rt)
		}
	}
}
