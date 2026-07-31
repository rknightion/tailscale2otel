package dns_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/dns"
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric the collector actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated from
// Catalog(), so this keeps the generated docs honest), and every emitted log
// event must be in LogCatalog().
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	tr := apistate.NewTracker()
	c := dns.New(&fakeCatalogAPI{cfg: &tsapi.DNSConfig{
		Nameservers: []tsapi.DNSResolver{{Address: "1.1.1.1", UseWithExitNode: true}},
		SplitDNS: map[string][]tsapi.DNSResolver{
			"corp.example.com": {{Address: "10.0.0.1"}},
		},
		SearchPaths:      []string{"example.com"},
		OverrideLocalDNS: true,
		MagicDNS:         true,
	}}, 0, dns.WithAPIState(tr))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	// The per-operation availability signals are declared once, in the shared
	// internal/apistate catalog (which internal/catalog aggregates), not per
	// collector — every collector emits the same two descriptors.
	for _, m := range append(dns.Catalog(), apistate.Catalog()...) {
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
			t.Errorf("emitted metric %q is not declared in dns.Catalog()", name)
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

	// Attribute-key drift guard: the loop above compares name/unit/description
	// only, so an emitted-but-undeclared attribute would silently rot the docs.
	telemetrytest.AssertCatalogAttrs(t, rec, append(dns.Catalog(), apistate.Catalog()...), dns.LogCatalog())

	logDeclared := map[string]bool{}
	for _, le := range dns.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in dns.LogCatalog()", lr.EventName)
		}
	}
}

// fakeCatalogAPI implements the narrow dns api interface for the catalog test.
type fakeCatalogAPI struct {
	cfg *tsapi.DNSConfig
}

func (f *fakeCatalogAPI) DNSConfiguration(_ context.Context) (*tsapi.DNSConfig, error) {
	return f.cfg, nil
}
