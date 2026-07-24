package objectstore_test

import (
	"io"
	"log/slog"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// discardLogger keeps the collector's WARN and ERROR lines out of test output.
// They are asserted through the emitted metrics instead, which is what an
// operator would actually alert on.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// skippedByReason totals the skipped counter per reason.
func skippedByReason(rec *telemetrytest.Recorder) map[string]float64 {
	out := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale2otel.objectstore.skipped") {
		out[p.Attrs["reason"]] += p.Value
	}
	return out
}

// lastGauge reads the most recent value of a gauge.
func lastGauge(rec *telemetrytest.Recorder, name string) float64 {
	pts := rec.MetricPoints(name)
	if len(pts) == 0 {
		return -1
	}
	return pts[len(pts)-1].Value
}
