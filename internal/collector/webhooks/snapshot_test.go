package webhooks

import (
	"context"
	"strings"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestSnapshotIsOptInChangeDrivenAndHeartbeatRefreshed(t *testing.T) {
	clock := time.Unix(1_000_000, 0).UTC()
	secret := "tskey-webhook-secret"
	api := &fakeAPI{hooks: []tsclient.Webhook{
		{
			EndpointID:       "wh-2",
			EndpointURL:      "https://hooks.example.invalid/two",
			ProviderType:     "slack",
			CreatorLoginName: "creator@example.invalid",
			Created:          clock.Add(-2 * time.Hour),
			LastModified:     clock.Add(-time.Hour),
			Subscriptions:    []tsclient.WebhookSubscriptionType{"userCreated", "nodeCreated"},
			Secret:           &secret,
		},
		{
			EndpointID:    "wh-1",
			ProviderType:  "discord",
			Created:       clock.Add(-3 * time.Hour),
			Subscriptions: []tsclient.WebhookSubscriptionType{"policyUpdate"},
		},
	}}
	c := New(api, 0, WithClock(func() time.Time { return clock }), WithSnapshot(true),
		WithSnapshotHeartbeat(time.Hour))
	rec := telemetrytest.New()

	collect := func(label string) []telemetrytest.LogRecord {
		t.Helper()
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("%s Collect: %v", label, err)
		}
		return rec.LogRecords()
	}

	logs := collect("first")
	if len(logs) != 1 {
		t.Fatalf("first snapshot logs = %d, want 1", len(logs))
	}
	first := logs[0]
	if first.EventName != EventWebhooksSnapshot {
		t.Fatalf("first event = %q, want %q", first.EventName, EventWebhooksSnapshot)
	}
	if got := first.Attrs["tailscale.snapshot.kind"]; got != "webhooks" {
		t.Errorf("snapshot kind = %q, want webhooks", got)
	}
	if got := first.Attrs["tailscale.snapshot.reason"]; got != "change" {
		t.Errorf("first snapshot reason = %q, want change", got)
	}
	if !strings.Contains(first.Body, `"webhooks"`) ||
		!strings.Contains(first.Body, `"endpointId":"wh-1"`) ||
		!strings.Contains(first.Body, `"subscriptions":["nodeCreated","userCreated"]`) {
		t.Fatalf("snapshot body = %q, want canonical safe webhook inventory", first.Body)
	}
	for _, forbidden := range []string{secret, "https://hooks.example.invalid", "creator@example.invalid"} {
		if strings.Contains(first.Body, forbidden) {
			t.Fatalf("snapshot body leaked forbidden value %q: %s", forbidden, first.Body)
		}
	}

	// API list order is not configuration. The canonical DTO must avoid a
	// spurious revision when the service returns the same endpoints reordered.
	api.hooks[0], api.hooks[1] = api.hooks[1], api.hooks[0]
	clock = clock.Add(30 * time.Minute)
	if logs = collect("quiet"); len(logs) != 1 {
		t.Fatalf("quiet snapshot logs = %d, want 1 cumulative record", len(logs))
	}

	clock = clock.Add(30 * time.Minute)
	logs = collect("heartbeat")
	if len(logs) != 2 {
		t.Fatalf("heartbeat snapshot logs = %d, want 2 cumulative records", len(logs))
	}
	if got := logs[1].Attrs["tailscale.snapshot.reason"]; got != "heartbeat" {
		t.Errorf("heartbeat snapshot reason = %q, want heartbeat", got)
	}

	api.hooks[0].Subscriptions = []tsclient.WebhookSubscriptionType{"nodeDeleted"}
	clock = clock.Add(time.Minute)
	logs = collect("change")
	if len(logs) != 3 {
		t.Fatalf("changed snapshot logs = %d, want 3 cumulative records", len(logs))
	}
	if got := logs[2].Attrs["tailscale.snapshot.reason"]; got != "change" {
		t.Errorf("changed snapshot reason = %q, want change", got)
	}
}

func TestSnapshotDisabledByDefault(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{hooks: sampleHooks()}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if logs := rec.LogRecords(); len(logs) != 0 {
		t.Fatalf("snapshot logs = %d, want 0 when opt-in is absent", len(logs))
	}
}
