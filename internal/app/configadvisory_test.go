package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// Startup advisories are logged through slog before the OTLP pipeline exists,
// so they never reach Loki. emitConfigAdvisories re-emits each one through the
// process emitter once it is up.
func TestEmitConfigAdvisories_OneWarnEventPerAdvisory(t *testing.T) {
	rec := telemetrytest.New()
	cfg := cfgWithWarning()
	want := cfg.Advisories()
	if len(want) == 0 {
		t.Fatal("cfgWithWarning() returned no advisories - test setup broken")
	}

	emitConfigAdvisories(rec.Emitter(), cfg)

	var got []telemetrytest.LogRecord
	for _, r := range rec.LogRecords() {
		if r.EventName == appcatalog.EventConfigAdvisory {
			got = append(got, r)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d %s events, want %d", len(got), appcatalog.EventConfigAdvisory, len(want))
	}
	for i, r := range got {
		if r.Body != want[i].Message {
			t.Errorf("event %d body = %q, want the advisory text %q", i, r.Body, want[i].Message)
		}
		if r.SeverityText != "WARN" {
			t.Errorf("event %d severity = %q, want WARN", i, r.SeverityText)
		}
		if k := r.Attrs[appcatalog.AttrConfigKey]; k != want[i].Key {
			t.Errorf("event %d %s = %q, want %q", i, appcatalog.AttrConfigKey, k, want[i].Key)
		}
	}
	telemetrytest.AssertCatalogAttrs(t, rec, appcatalog.Catalog(), appcatalog.LogCatalog())
}

func TestEmitConfigAdvisories_NoAdvisoriesNoEvents(t *testing.T) {
	rec := telemetrytest.New()
	emitConfigAdvisories(rec.Emitter(), validCfg())
	if n := len(rec.LogRecords()); n != 0 {
		t.Fatalf("got %d log records for a config with no advisories, want 0", n)
	}
}
