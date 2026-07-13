package services_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/services"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

type catalogFakeAPI struct{}

func (catalogFakeAPI) Services(context.Context) ([]tsapi.VIPService, error) {
	return []tsapi.VIPService{{Name: "svc:argocd", Ports: []string{"tcp:443"}}}, nil
}

func (catalogFakeAPI) ServiceHosts(context.Context, string) ([]tsapi.ServiceHost, error) {
	return []tsapi.ServiceHost{{NodeID: "n1", ApprovalLevel: "approved:auto", Configured: "ready"}}, nil
}

func (catalogFakeAPI) ServiceAddrs(context.Context) ([]tsapi.ServiceAddr, error) {
	return nil, nil
}

// TestCatalogMatchesEmitted drives collect_hosts so all three declared metrics
// are emitted and checked against the catalog.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	c := services.New(catalogFakeAPI{}, 0, services.WithCollectHosts(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range services.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in services.Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
	}

	// Attribute-drift guard (#126): every emitted attribute must be declared.
	telemetrytest.AssertCatalogAttrs(t, rec, services.Catalog(), services.LogCatalog())
}
