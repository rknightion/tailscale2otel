package webhooks_test

import (
	"context"
	"testing"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/webhooks"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type catalogFakeAPI struct{ hooks []tsclient.Webhook }

func (f *catalogFakeAPI) Webhooks(context.Context) ([]tsclient.Webhook, error) { return f.hooks, nil }

// TestCatalogMatchesEmitted is the declaration<->emission drift guard. A
// non-empty webhook ensures both declared metrics are emitted.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	tr := apistate.NewTracker()
	c := webhooks.New(&catalogFakeAPI{hooks: []tsclient.Webhook{{
		EndpointID:    "wh-1",
		ProviderType:  "slack",
		Subscriptions: []tsclient.WebhookSubscriptionType{"nodeCreated"},
	}}}, 0, webhooks.WithAPIState(tr), webhooks.WithSnapshot(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	// The per-operation availability signals (tailscale2otel.api.availability,
	// tailscale2otel.api.last_probe) are declared once, in the shared
	// internal/apistate catalog (which internal/catalog aggregates), not per
	// collector — every collector emits the same two descriptors.
	for _, m := range append(webhooks.Catalog(), apistate.Catalog()...) {
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

	// Attribute-key drift guard: the loop above compares name/unit/description
	// only, so an emitted-but-undeclared attribute would silently rot the docs.
	telemetrytest.AssertCatalogAttrs(t, rec,
		append(webhooks.Catalog(), apistate.Catalog()...), webhooks.LogCatalog())

	logDeclared := map[string]bool{}
	for _, le := range webhooks.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in webhooks.LogCatalog()", lr.EventName)
		}
	}

	// The API-state signals belong to internal/apistate's catalog, NOT this
	// collector's — assert they are emitted but deliberately absent from
	// webhooks.Catalog() itself, so a future "fix" that copies them in
	// double-declares them in docs/metrics.md and fails loudly instead of
	// silently.
	ownDeclared := map[string]bool{}
	for _, m := range webhooks.Catalog() {
		ownDeclared[m.Name] = true
	}
	for _, name := range []string{apistate.MetricAvailability, apistate.MetricLastProbe} {
		if len(rec.MetricPoints(name)) == 0 {
			t.Errorf("%s not emitted with a Coverage/tracker wired", name)
		}
		if ownDeclared[name] {
			t.Errorf("%s must be declared by internal/apistate, not webhooks.Catalog()", name)
		}
	}
}
