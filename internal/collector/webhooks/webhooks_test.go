package webhooks

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

type fakeAPI struct {
	hooks []tsclient.Webhook
	err   error
}

func (f *fakeAPI) Webhooks(context.Context) ([]tsclient.Webhook, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.hooks, nil
}

var _ collector.SnapshotCollector = (*Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if c.Name() != "webhooks" {
		t.Fatalf("Name() = %q, want webhooks", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
}

func sampleHooks() []tsclient.Webhook {
	secret := "tskey-webhook-SECRET"
	return []tsclient.Webhook{
		{
			EndpointID:       "wh-1",
			EndpointURL:      "https://hook.example/abc",
			ProviderType:     "slack",
			CreatorLoginName: "creator@example.com",
			Subscriptions:    []tsclient.WebhookSubscriptionType{"nodeCreated", "nodeDeleted"},
			Secret:           &secret,
		},
		{
			EndpointID:    "wh-2",
			EndpointURL:   "https://hook.example/def",
			ProviderType:  "",
			Subscriptions: []tsclient.WebhookSubscriptionType{"userApproved"},
		},
	}
}

func TestCollectEmitsCountAndSubscriptions(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{hooks: sampleHooks()}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	cnt := rec.MetricPoints("tailscale.webhook_endpoints.count")
	if len(cnt) != 1 || cnt[0].Value != 2 {
		t.Fatalf("webhook_endpoints.count = %+v, want one point value 2", cnt)
	}

	subs := rec.MetricPoints("tailscale.webhook_endpoint.subscriptions")
	byID := map[string]float64{}
	for _, p := range subs {
		// Fence: never leak URL, secret, or creator login.
		for k, v := range p.Attrs {
			switch v {
			case "https://hook.example/abc", "https://hook.example/def", "creator@example.com", "tskey-webhook-SECRET":
				t.Fatalf("sensitive value leaked into attr %q=%q", k, v)
			}
		}
		byID[p.Attrs["tailscale.webhook_endpoint.id"]] = p.Value
	}
	if len(subs) != 2 {
		t.Fatalf("subscriptions points = %d, want 2", len(subs))
	}
	if byID["wh-1"] != 2 {
		t.Errorf("wh-1 subscriptions = %v, want 2", byID["wh-1"])
	}
	if byID["wh-2"] != 1 {
		t.Errorf("wh-2 subscriptions = %v, want 1", byID["wh-2"])
	}
}

func TestCollectEmptyWebhooks(t *testing.T) {
	rec := telemetrytest.New()
	// nil slice (the API returns {"webhooks":null} when none are configured).
	if err := New(&fakeAPI{hooks: nil}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	cnt := rec.MetricPoints("tailscale.webhook_endpoints.count")
	if len(cnt) != 1 || cnt[0].Value != 0 {
		t.Fatalf("count = %+v, want one point value 0", cnt)
	}
	if subs := rec.MetricPoints("tailscale.webhook_endpoint.subscriptions"); len(subs) != 0 {
		t.Fatalf("subscriptions points = %d, want 0 when no webhooks", len(subs))
	}
}

func TestPerEntityOffDropsSubscriptions(t *testing.T) {
	rec := telemetrytest.New()
	c := New(&fakeAPI{hooks: sampleHooks()}, 0, WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if cnt := rec.MetricPoints("tailscale.webhook_endpoints.count"); len(cnt) != 1 || cnt[0].Value != 2 {
		t.Fatalf("count = %+v, want value 2", cnt)
	}
	if subs := rec.MetricPoints("tailscale.webhook_endpoint.subscriptions"); len(subs) != 0 {
		t.Fatalf("subscriptions points = %d, want 0 when per_entity off", len(subs))
	}
}

func TestCollectPropagatesError(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{err: context.DeadlineExceeded}, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("expected error, got nil")
	}
}
