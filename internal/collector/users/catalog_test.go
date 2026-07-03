package users_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/collector/users"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric the collector actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated from
// Catalog(), so this keeps the generated docs honest), and every emitted log
// event must be in LogCatalog(). The drive recipe mirrors users_test.go and
// supplies a user with a non-zero LastSeen so user.last_seen also emits.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range users.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in users.Catalog()", name)
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
	for _, le := range users.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in users.LogCatalog()", lr.EventName)
		}
	}

	// Attribute-drift guard (#126): every emitted attribute must be declared.
	telemetrytest.AssertCatalogAttrs(t, rec, users.Catalog(), users.LogCatalog())
}
