package telemetry_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

func TestEmitExportStats(t *testing.T) {
	rec := telemetrytest.New()
	telemetry.EmitExportStats(rec.Emitter(), 10, 4) // deltas
	dp := rec.MetricPoints("tailscale2otel.export.datapoints")
	lr := rec.MetricPoints("tailscale2otel.export.log_records")
	if len(dp) != 1 || dp[0].Value != 10 || dp[0].Unit != "{datapoint}" || !dp[0].Monotonic {
		t.Errorf("export.datapoints = %+v", dp)
	}
	if len(lr) != 1 || lr[0].Value != 4 || lr[0].Unit != "{record}" || !lr[0].Monotonic {
		t.Errorf("export.log_records = %+v", lr)
	}

	rec2 := telemetrytest.New()
	telemetry.EmitExportStats(rec2.Emitter(), 0, 0) // zero deltas emit nothing
	if n := len(rec2.MetricPoints("tailscale2otel.export.datapoints")); n != 0 {
		t.Errorf("zero delta emitted %d datapoints points, want 0", n)
	}
	if n := len(rec2.MetricPoints("tailscale2otel.export.log_records")); n != 0 {
		t.Errorf("zero delta emitted %d log_records points, want 0", n)
	}
}
