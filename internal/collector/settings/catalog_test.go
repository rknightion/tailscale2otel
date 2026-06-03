package settings_test

import (
	"context"
	"testing"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/settings"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// catalogFakeAPI is a minimal settings api implementation for the external
// catalog test (the internal-package fakeAPI is not visible here).
type catalogFakeAPI struct {
	settings *tsclient.TailnetSettings
}

func (f *catalogFakeAPI) TailnetSettings(context.Context) (*tsclient.TailnetSettings, error) {
	return f.settings, nil
}

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric the collector actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated from
// Catalog(), so this keeps the generated docs honest), and every emitted log
// event must be in LogCatalog(). The mixed true/false bools plus a non-zero key
// duration ensure both declared metrics are emitted.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	c := settings.New(&catalogFakeAPI{settings: &tsclient.TailnetSettings{
		DevicesApprovalOn:           true,
		DevicesAutoUpdatesOn:        false,
		UsersApprovalOn:             true,
		NetworkFlowLoggingOn:        false,
		RegionalRoutingOn:           true,
		PostureIdentityCollectionOn: false,
		DevicesKeyDurationDays:      90,
	}}, 0)

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range settings.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in settings.Catalog()", name)
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

	logDeclared := map[string]bool{}
	for _, le := range settings.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in settings.LogCatalog()", lr.EventName)
		}
	}
}
