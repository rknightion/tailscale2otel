package contacts_test

import (
	"context"
	"testing"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/contacts"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

type catalogFakeAPI struct{ contacts *tsclient.Contacts }

func (f *catalogFakeAPI) Contacts(context.Context) (*tsclient.Contacts, error) {
	return f.contacts, nil
}

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric emitted must be declared in Catalog() with a matching unit, instrument
// and description (docs/metrics.md is generated from Catalog()).
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	c := contacts.New(&catalogFakeAPI{contacts: &tsclient.Contacts{
		Account:  tsclient.Contact{NeedsVerification: true},
		Support:  tsclient.Contact{NeedsVerification: false},
		Security: tsclient.Contact{NeedsVerification: true},
	}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range contacts.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in contacts.Catalog()", name)
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
