package audit_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric the processor actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated from
// Catalog(), so this keeps the generated docs honest), and every emitted log
// event must be in LogCatalog().
//
// This package emits only its own signals (no delegation): driving
// p.Process(event) emits the two counters (tailscale.config.audit.events,
// tailscale.config.audit.changes) and the log event (tailscale.config.audit).
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor()

	p.Process(audit.Event{
		EventTime:    time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
		Type:         "CONFIG",
		EventGroupID: "abc123",
		Origin:       "ADMIN_CONSOLE",
		Actor: audit.Actor{
			ID:          "u1",
			Type:        "USER",
			LoginName:   "a@example.com",
			DisplayName: "Lion",
		},
		Target: audit.Target{
			ID:       "n1",
			Name:     "node.ts.net",
			Type:     "NODE",
			Property: "ALLOWED_IPS",
		},
		Action:        "CREATE",
		ActionDetails: "x",
	}, rec.Emitter())

	// A clearly-curated event so the changes counter is definitely emitted and
	// its unit/description/instrument are checked against the catalog.
	p.Process(audit.Event{
		EventTime: time.Date(2024, 6, 6, 15, 25, 27, 0, time.UTC),
		Type:      "CONFIG",
		Origin:    "ADMIN_CONSOLE",
		Actor:     audit.Actor{ID: "u1", Type: "USER", LoginName: "a@example.com"},
		Target:    audit.Target{ID: "t1", Type: "TAILNET", Property: "ACL"},
		Action:    "UPDATE",
	}, rec.Emitter())

	declared := map[string]metricdoc.Metric{}
	for _, m := range audit.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in audit.Catalog()", name)
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
	for _, le := range audit.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in audit.LogCatalog()", lr.EventName)
		}
	}
}
