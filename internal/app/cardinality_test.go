package app

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// A non-positive metric interval (e.g. otlp.metric_interval: 0s) must not crash
// the cardinality reporter: time.NewTicker(0) panics, so the reporter has to
// clamp to a positive fallback the way its sibling reporters (runtime, dedup)
// do. With a pre-canceled context the reporter should create its ticker and
// then return on ctx.Done() rather than panic.
func TestRunCardinalityReporter_NonPositiveIntervalDoesNotPanic(t *testing.T) {
	rec := telemetrytest.New()
	card := telemetry.NewCardinalityTracker() // non-nil tracker => reporter proceeds past the nil guard

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runCardinalityReporter(ctx, rec.Emitter(), card, 0)
}
