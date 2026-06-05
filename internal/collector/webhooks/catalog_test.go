package webhooks_test

import (
	"context"
	"testing"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/webhooks"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

type catalogFakeAPI struct{ hooks []tsclient.Webhook }

func (f *catalogFakeAPI) Webhooks(context.Context) ([]tsclient.Webhook, error) { return f.hooks, nil }

// TestCatalogMatchesEmitted is the declaration<->emission drift guard. A
// non-empty webhook ensures both declared metrics are emitted.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	c := webhooks.New(&catalogFakeAPI{hooks: []tsclient.Webhook{{
		EndpointID:    "wh-1",
		ProviderType:  "slack",
		Subscriptions: []tsclient.WebhookSubscriptionType{"nodeCreated"},
	}}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range webhooks.Catalog() {
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
			t.Errorf("emitted metric %q is not declared in webhooks.Catalog()", name)
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
