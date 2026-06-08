package telemetry_test

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard for the
// telemetry package's self-observability metrics: every metric these helpers
// actually emit must be declared in telemetry.Catalog() with a matching unit,
// instrument, and description (docs/metrics.md is generated from Catalog(), so
// this keeps the generated docs honest).
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()

	telemetry.EmitBuildInfo(rec.Emitter(), "go1.26")
	restore := telemetry.InstallExportErrorHandler(rec.Emitter(), nil)
	defer restore()
	otel.Handle(errors.New("boom"))

	// Emit the cardinality self-metric too so docSeriesActive is exercised by the
	// drift guard (observe one series, then report it through the recorder).
	tr := telemetry.NewCardinalityTracker()
	tr.Observe("tailscale.example", telemetry.Attrs{"a": "x"})
	tr.Report(rec.Emitter())

	// Exercise the export.duration histogram so docExportDuration is covered by
	// the declaration<->emission drift guard.
	telemetry.EmitExportDuration(rec.Emitter(), "metrics", "success", 0.01)

	declared := map[string]metricdoc.Metric{}
	for _, m := range telemetry.Catalog() {
		declared[m.Name] = m
	}

	for _, name := range rec.MetricNames() {
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in telemetry.Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
		wantCounter := d.Instrument == metricdoc.Counter
		gotCounter := p0.Kind == "sum" && p0.Monotonic
		if wantCounter != gotCounter {
			t.Errorf("%s: catalog instrument %q but emitted kind=%q monotonic=%v", name, d.Instrument, p0.Kind, p0.Monotonic)
		}
	}
}
