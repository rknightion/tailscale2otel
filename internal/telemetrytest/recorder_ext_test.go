package telemetrytest_test

import (
	"reflect"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// TestRecorderHistogram verifies the Recorder surfaces histogram data points
// (count, bounds, bucket counts) so collector tests can assert distributions.
func TestRecorderHistogram(t *testing.T) {
	rec := telemetrytest.New()
	bounds := []float64{0, 7, 30}
	rec.Emitter().Histogram("h.test", "d", "desc", -1, bounds, nil)
	rec.Emitter().Histogram("h.test", "d", "desc", 10, bounds, nil)

	pts := rec.MetricPoints("h.test")
	if len(pts) != 1 {
		t.Fatalf("points = %d, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "histogram" {
		t.Errorf("Kind = %q, want histogram", p.Kind)
	}
	if p.Count != 2 {
		t.Errorf("Count = %d, want 2", p.Count)
	}
	// (-inf,0]=1, (0,7]=0, (7,30]=1, (30,+inf]=0
	if want := []uint64{1, 0, 1, 0}; !reflect.DeepEqual(p.BucketCounts, want) {
		t.Errorf("BucketCounts = %v, want %v", p.BucketCounts, want)
	}
	if want := []float64{0, 7, 30}; !reflect.DeepEqual(p.Bounds, want) {
		t.Errorf("Bounds = %v, want %v", p.Bounds, want)
	}
}

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
