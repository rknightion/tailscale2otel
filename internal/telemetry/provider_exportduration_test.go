package telemetry

import (
	"context"
	"io"
	"testing"
	"time"
)

// TestProviderEmitsExportDuration is a smoke test that verifies the export.duration
// observer path executes without panic or error. It cannot assert the emitted
// histogram value directly because the existing provider-test harness uses a
// stdout exporter written to io.Discard — there is no in-memory reader exposed by
// the production NewProvider path. Behavioral coverage of the emitted histogram
// lives in the unit tests TestEmitExportDuration (selfobs_test.go) and
// TestCountingMetricExporterObservesDuration (export_counting_test.go).
//
// The test confirms:
//  1. A self-obs-enabled Provider can be constructed with observers wired.
//  2. Driving an export cycle via Shutdown (which flushes the exporters) invokes
//     the observers on both metricCounter and logCounter without panicking.
//  3. Shutdown returns nil (no error from the observer or the exporter).
func TestProviderEmitsExportDuration(t *testing.T) {
	ctx := context.Background()

	p, err := NewProvider(ctx, Options{
		Protocol:       "stdout",
		StdoutWriter:   io.Discard,
		ServiceName:    "t",
		SelfObsEnabled: true,
		MetricInterval: time.Hour, // rely on Shutdown flush, not the interval
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// Confirm observers were wired (non-nil pointer stored in the decorators).
	if p.metricCounter == nil {
		t.Fatal("metricCounter is nil with SelfObsEnabled=true")
	}
	if p.logCounter == nil {
		t.Fatal("logCounter is nil with SelfObsEnabled=true")
	}
	if p.metricCounter.obs.Load() == nil {
		t.Error("metricCounter observer not set after NewProvider")
	}
	if p.logCounter.obs.Load() == nil {
		t.Error("logCounter observer not set after NewProvider")
	}

	// Emit something so the flush has at least one data point, exercising the
	// metric-counter observer code path.
	p.emitter.Counter("t.probe", "1", "probe", 1, nil)
	p.emitter.LogEvent(Event{Body: "probe"})

	// Shutdown flushes exporters → Export() fires → observers fire → EmitExportDuration
	// records the histogram. No panic, no error.
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
