package collector

import (
	"context"
	"os"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

// RunCheckpointReporter emits checkpoint file health metrics (disk size and
// persist age) immediately and then on each interval until ctx is canceled. If
// interval is <= 0 it defaults to 60 seconds. The path argument is the
// checkpoint file path; if the file does not exist yet no metrics are emitted
// for that tick (to avoid misleading zeros before the first persist).
func RunCheckpointReporter(ctx context.Context, e telemetry.Emitter, path string, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	emit := func() { emitCheckpointStats(e, path) }
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

// emitCheckpointStats stats the checkpoint file at path and emits two gauges:
// checkpoint.disk.size (bytes) and checkpoint.persist.age (seconds since mtime).
// On any os.Stat error (including file-not-found) it returns without emitting,
// so no misleading zeros appear before the first successful persist.
func emitCheckpointStats(e telemetry.Emitter, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	e.Gauge(docCheckpointDiskSize.Name, docCheckpointDiskSize.Unit, docCheckpointDiskSize.Description,
		float64(info.Size()), nil)
	e.Gauge(docCheckpointPersistAge.Name, docCheckpointPersistAge.Unit, docCheckpointPersistAge.Description,
		time.Since(info.ModTime()).Seconds(), nil)
}
