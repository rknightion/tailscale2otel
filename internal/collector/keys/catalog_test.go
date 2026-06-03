package keys_test

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric this collector actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated from
// Catalog(), so this keeps the generated docs honest), and every emitted log
// event must be in LogCatalog(). This collector emits only its own signals, so
// the plain template applies.
func TestCatalogMatchesEmitted(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsclient.Key{
		// Expires within the 24h warn window => drives key.expiry metric AND
		// the key.expiring WARN log.
		reusableKey("k1", now.Add(1*time.Hour)),
		// A second, distinct key to populate keys.count buckets.
		{ID: "k2", Description: "oneoff", Expires: now.Add(48 * time.Hour)},
	}}, 0, 24*time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range keys.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in keys.Catalog()", name)
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
	for _, le := range keys.LogCatalog() {
		logDeclared[le.Name] = true
	}
	sawExpiring := false
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "" {
			continue
		}
		if lr.EventName == keys.EventExpiring {
			sawExpiring = true
		}
		if !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in keys.LogCatalog()", lr.EventName)
		}
	}
	if !sawExpiring {
		t.Fatalf("test did not drive the %q log event; check the warn window", keys.EventExpiring)
	}
}
