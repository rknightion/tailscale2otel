package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

func TestEmitExportDelta(t *testing.T) {
	rec := telemetrytest.New()
	var last telemetry.ExportStats

	emitExportDelta(rec.Emitter(), telemetry.ExportStats{Datapoints: 30, LogRecords: 5}, &last)
	if got := rec.MetricPoints("tailscale2otel.export.datapoints"); len(got) != 1 || got[0].Value != 30 {
		t.Fatalf("first delta datapoints = %+v, want 30", got)
	}
	if last != (telemetry.ExportStats{Datapoints: 30, LogRecords: 5}) {
		t.Fatalf("last not advanced: %+v", last)
	}

	emitExportDelta(rec.Emitter(), telemetry.ExportStats{Datapoints: 50, LogRecords: 5}, &last)
	// cumulative recorder: datapoints series now 30 + (50-30) = 50; log_records unchanged (delta 0)
	dp := rec.MetricPoints("tailscale2otel.export.datapoints")
	if len(dp) != 1 || dp[0].Value != 50 {
		t.Fatalf("second delta datapoints cumulative = %+v, want 50", dp)
	}
}
