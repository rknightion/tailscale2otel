package telemetrytest_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestMetricPointCapturesDescription verifies the Recorder surfaces a metric's
// description (needed by the metric-catalog generator/validator to derive
// docs/metrics.md from code).
func TestMetricPointCapturesDescription(t *testing.T) {
	rec := telemetrytest.New()
	rec.Emitter().Counter("test.metric", "By", "Total widgets transferred", 1, telemetry.Attrs{})

	pts := rec.MetricPoints("test.metric")
	if len(pts) != 1 {
		t.Fatalf("MetricPoints = %d, want 1", len(pts))
	}
	if pts[0].Description != "Total widgets transferred" {
		t.Fatalf("Description = %q, want %q", pts[0].Description, "Total widgets transferred")
	}
}
