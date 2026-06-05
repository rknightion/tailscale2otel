package postureintegrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector/postureintegrations"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

type catalogFakeAPI struct{ ints []tsapi.PostureIntegration }

func (f *catalogFakeAPI) PostureIntegrations(context.Context) ([]tsapi.PostureIntegration, error) {
	return f.ints, nil
}

// TestCatalogMatchesEmitted is the declaration<->emission drift guard. A
// non-zero LastSync ensures all five declared metrics are emitted.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	c := postureintegrations.New(&catalogFakeAPI{ints: []tsapi.PostureIntegration{{
		ID: "p1", Provider: "intune",
		Status: tsapi.PostureIntegrationStatus{
			LastSync: time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC), MatchedCount: 4, PossibleMatchedCount: 5, ProviderHostCount: 10,
		},
	}}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range postureintegrations.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
	}
}
