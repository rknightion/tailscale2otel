package postureintegrations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

func TestSnapshotIsOptInChangeDrivenAndHeartbeatRefreshed(t *testing.T) {
	clock := time.Unix(1_000_000, 0).UTC()
	api := &fakeAPI{ints: []tsapi.PostureIntegration{
		{
			ID:       "posture-2",
			Provider: "intune",
			Status: tsapi.PostureIntegrationStatus{
				LastSync:             clock.Add(-time.Minute),
				MatchedCount:         4,
				PossibleMatchedCount: 5,
				ProviderHostCount:    10,
				Error:                "tenant secret must not be logged",
			},
		},
		{ID: "posture-1", Provider: "jamfpro"},
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
	if first.EventName != EventPostureIntegrationsSnapshot {
		t.Fatalf("first event = %q, want %q", first.EventName, EventPostureIntegrationsSnapshot)
	}
	if got := first.Attrs["tailscale.snapshot.kind"]; got != "posture_integrations" {
		t.Errorf("snapshot kind = %q, want posture_integrations", got)
	}
	if got := first.Attrs["tailscale.snapshot.reason"]; got != "change" {
		t.Errorf("first snapshot reason = %q, want change", got)
	}
	if !strings.Contains(first.Body, `"integrations"`) ||
		!strings.Contains(first.Body, `"id":"posture-1"`) ||
		!strings.Contains(first.Body, `"error":true`) {
		t.Fatalf("snapshot body = %q, want canonical safe posture inventory", first.Body)
	}
	if strings.Contains(first.Body, "tenant secret must not be logged") {
		t.Fatalf("snapshot body leaked raw sync error: %s", first.Body)
	}

	// API list order is not configuration. Reordering the same integrations
	// must not create a false snapshot revision.
	api.ints[0], api.ints[1] = api.ints[1], api.ints[0]
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

	api.ints[1].Status.Error = ""
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
	if err := New(&fakeAPI{ints: []tsapi.PostureIntegration{{ID: "p1", Provider: "intune"}}}, 0).
		Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if logs := rec.LogRecords(); len(logs) != 0 {
		t.Fatalf("snapshot logs = %d, want 0 when opt-in is absent", len(logs))
	}
}
