// Package collector export_test.go exposes internal symbols for white-box tests
// in the collector_test package. This file is compiled only during `go test`.
package collector

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// RunTick is a test shim that calls the unexported runTick method so external
// tests (package collector_test) can drive a single tick synchronously without
// going through the goroutine scheduler.
func (s *Scheduler) RunTick(ctx context.Context, e Entry, lastSuccess *time.Time) {
	s.runTick(ctx, e, lastSuccess)
}

// EmitCheckpointStats exposes emitCheckpointStats for package-external tests.
func EmitCheckpointStats(e telemetry.Emitter, path string) {
	emitCheckpointStats(e, path)
}
