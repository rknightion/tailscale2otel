package nodemetrics_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

const (
	discoverySuccessName  = "tailscale2otel.nodemetrics.discovery.success"
	discoveredTargetsName = "tailscale2otel.nodemetrics.discovery.targets"
)

// TestCatalog_DiscoveryMetricsDeclaredAndEmitted is the drift guard for the two
// discovery-health gauges: when a Discoverer is configured they must be declared
// in Catalog() AND emitted every Collect, with matching unit/instrument.
func TestCatalog_DiscoveryMetricsDeclaredAndEmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 1\n"))
	}))
	defer srv.Close()

	fake := &fakeDiscoverer{targets: []nodemetrics.Target{{URL: srv.URL, Instance: "disc"}}}
	c := nodemetrics.New(nodemetrics.Options{Discoverer: fake, DiscoveryInterval: time.Minute})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range nodemetrics.Catalog() {
		declared[m.Name] = m
	}
	for _, name := range []string{discoverySuccessName, discoveredTargetsName} {
		d, ok := declared[name]
		if !ok {
			t.Errorf("%q is not declared in nodemetrics.Catalog()", name)
			continue
		}
		pts := rec.MetricPoints(name)
		if len(pts) != 1 {
			t.Fatalf("%q points = %d, want 1", name, len(pts))
		}
		if pts[0].Kind != "gauge" {
			t.Errorf("%s kind = %q, want gauge", name, pts[0].Kind)
		}
		if pts[0].Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, pts[0].Unit, d.Unit)
		}
		if pts[0].Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, pts[0].Description, d.Description)
		}
	}
	if pts := rec.MetricPoints(discoverySuccessName); pts[0].Value != 1 {
		t.Errorf("discovery.success = %v, want 1 (fake discovered successfully)", pts[0].Value)
	}
	if pts := rec.MetricPoints(discoveredTargetsName); pts[0].Value != 1 {
		t.Errorf("discovery.targets = %v, want 1 (one discovered target)", pts[0].Value)
	}
}

// TestDiscovery_HealthGaugeZeroOnError: a failed discovery reports success=0 and,
// with no static targets, targets=0 — and the health gauges are still emitted
// (and Collect returns nil) even though the active set is empty.
func TestDiscovery_HealthGaugeZeroOnError(t *testing.T) {
	fake := &fakeDiscoverer{err: errors.New("boom")}
	c := nodemetrics.New(nodemetrics.Options{Discoverer: fake, DiscoveryInterval: time.Minute})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v, want nil (empty active set)", err)
	}
	if pts := rec.MetricPoints(discoverySuccessName); len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("discovery.success = %+v, want single 0 (discovery failed)", pts)
	}
	if pts := rec.MetricPoints(discoveredTargetsName); len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("discovery.targets = %+v, want single 0 (no targets)", pts)
	}
}

// TestCatalogMatchesEmitted is the declaration<->emission drift guard for the
// only statically-enumerable metric this collector emits: the per-target
// tailscale.node.up health gauge. The scraper also FORWARDS every scraped
// tailscaled_* series VERBATIM with runtime-derived names/units, which is not
// statically enumerable and so is NOT in Catalog(); those forwarded series are
// ignored here (only tailscale.node.up is checked). docs/metrics.md is generated
// from Catalog(), so this keeps the generated docs honest. There are no log
// events.
func TestCatalogMatchesEmitted(t *testing.T) {
	const upName = "tailscale.node.up"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE node_load gauge\nnode_load 0.5\n"))
	}))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range nodemetrics.Catalog() {
		declared[m.Name] = m
	}

	// Drift check: the ONLY emitted metric we treat as catalog-bound is
	// tailscale.node.up. All other emitted series are forwarded tailscaled_*
	// names that are deliberately not in Catalog().
	for _, name := range rec.MetricNames() {
		if name != upName {
			continue // forwarded, runtime-named series are not catalog-bound
		}
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in nodemetrics.Catalog()", name)
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

	// Assert tailscale.node.up IS emitted and declared with the expected unit,
	// gauge instrument, and description.
	upPts := rec.MetricPoints(upName)
	if len(upPts) == 0 {
		t.Fatalf("expected %q to be emitted on scrape, got none", upName)
	}
	d, ok := declared[upName]
	if !ok {
		t.Fatalf("%q is not declared in nodemetrics.Catalog()", upName)
	}
	if d.Unit != "1" {
		t.Errorf("%s: catalog unit %q, want %q", upName, d.Unit, "1")
	}
	if d.Instrument != metricdoc.Gauge {
		t.Errorf("%s: catalog instrument %q, want gauge", upName, d.Instrument)
	}
	p0 := upPts[0]
	if p0.Kind != "gauge" {
		t.Errorf("%s: emitted kind %q, want gauge", upName, p0.Kind)
	}
	if p0.Unit != d.Unit {
		t.Errorf("%s: emitted unit %q != catalog unit %q", upName, p0.Unit, d.Unit)
	}
	if p0.Description != d.Description {
		t.Errorf("%s: emitted description %q != catalog description %q", upName, p0.Description, d.Description)
	}

	// No log events are emitted by this collector.
	logDeclared := map[string]bool{}
	for _, le := range nodemetrics.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in nodemetrics.LogCatalog()", lr.EventName)
		}
	}
}
