package oauthapps_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/collector/oauthapps"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric this collector actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated
// from Catalog(), so this keeps the generated docs honest), and every emitted
// log event must be in LogCatalog().
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{apps: []tsapi.OAuthApp{
		{ID: "app1", Name: "provisioner", Scopes: []string{"auth_keys:create"}, AllowedNodeAttributes: []string{"custom:x"}},
	}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range oauthapps.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in oauthapps.Catalog()", name)
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
	for _, le := range oauthapps.LogCatalog() {
		logDeclared[le.Name] = true
	}
	sawInfo := false
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "" {
			continue
		}
		if lr.EventName == oauthapps.EventAppInfo {
			sawInfo = true
		}
		if !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in oauthapps.LogCatalog()", lr.EventName)
		}
	}
	if !sawInfo {
		t.Fatalf("test did not drive the %q log event", oauthapps.EventAppInfo)
	}

	// Attribute-drift guard (#126): every emitted metric/log attribute must be
	// declared in the catalog, so docs/metrics.md can't silently drift.
	telemetrytest.AssertCatalogAttrs(t, rec, oauthapps.Catalog(), oauthapps.LogCatalog())
}
