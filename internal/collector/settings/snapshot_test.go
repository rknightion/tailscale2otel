package settings

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

func TestSnapshotIsOptInChangeDrivenAndHeartbeatRefreshed(t *testing.T) {
	clock := time.Unix(1_000_000, 0).UTC()
	link := "https://policy.example.invalid/team/policy"
	api := &fakeAPI{settings: &tsapi.TailnetSettings{
		DevicesApprovalOn:                      true,
		DevicesKeyDurationDays:                 90,
		UsersRoleAllowedToJoinExternalTailnets: "member",
		ACLsExternalLink:                       &link,
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
	if first.EventName != EventSettingsSnapshot {
		t.Fatalf("first event = %q, want %q", first.EventName, EventSettingsSnapshot)
	}
	if got := first.Attrs["tailscale.snapshot.kind"]; got != "settings" {
		t.Errorf("snapshot kind = %q, want settings", got)
	}
	if got := first.Attrs["tailscale.snapshot.reason"]; got != "change" {
		t.Errorf("first snapshot reason = %q, want change", got)
	}
	if !strings.Contains(first.Body, `"devicesApprovalOn":true`) ||
		!strings.Contains(first.Body, `"devicesKeyDurationDays":90`) ||
		!strings.Contains(first.Body, `"aclsExternalLinkSet":true`) {
		t.Fatalf("snapshot body = %q, want the complete safe settings state", first.Body)
	}
	if strings.Contains(first.Body, link) {
		t.Fatalf("snapshot body leaked ACL link %q", link)
	}

	clock = clock.Add(time.Hour)
	logs = collect("heartbeat")
	if len(logs) != 2 {
		t.Fatalf("heartbeat snapshot logs = %d, want 2 cumulative records", len(logs))
	}
	if got := logs[1].Attrs["tailscale.snapshot.reason"]; got != "heartbeat" {
		t.Errorf("heartbeat snapshot reason = %q, want heartbeat", got)
	}

	api.settings = &tsapi.TailnetSettings{DevicesApprovalOn: false}
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
	if err := New(&fakeAPI{settings: &tsapi.TailnetSettings{}}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if logs := rec.LogRecords(); len(logs) != 0 {
		t.Fatalf("snapshot logs = %d, want 0 when opt-in is absent", len(logs))
	}
}

func TestSnapshotEmitterInitializationErrorIsReturned(t *testing.T) {
	api := &fakeAPI{settings: &tsapi.TailnetSettings{}}
	err := New(api, 0, WithSnapshot(true, 1)).Collect(context.Background(), telemetrytest.New().Emitter())
	if err == nil {
		t.Fatal("Collect returned nil with an invalid snapshot body limit")
	}
}
