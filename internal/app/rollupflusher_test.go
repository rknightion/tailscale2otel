package app

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// A non-positive export interval (otlp.metric_interval: 0s) must not crash the
// rollup flusher: time.NewTicker(0) panics, so it clamps to a positive fallback
// like its sibling reporters. With a pre-canceled context it creates the ticker
// and returns on ctx.Done() rather than panic.
func TestRunRollupFlusher_NonPositiveIntervalDoesNotPanic(t *testing.T) {
	rec := telemetrytest.New()
	proc := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{FlowMetricsMode: "rollup"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runRollupFlusher(ctx, proc, rec.Emitter(), 0)
}

// A nil processor (defensive) must return immediately without panicking.
func TestRunRollupFlusher_NilProcDoesNotPanic(t *testing.T) {
	rec := telemetrytest.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runRollupFlusher(ctx, nil, rec.Emitter(), 0)
}
