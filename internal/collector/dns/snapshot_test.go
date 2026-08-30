package dns

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
	api := &fakeAPI{cfg: &tsapi.DNSConfig{
		Nameservers:      []tsapi.DNSResolver{{Address: "192.0.2.53", UseWithExitNode: true}},
		SplitDNS:         map[string][]tsapi.DNSResolver{"corp.example": {{Address: "192.0.2.54"}}},
		SearchPaths:      []string{"example"},
		OverrideLocalDNS: true,
		MagicDNS:         true,
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
	if first.EventName != EventDNSSnapshot {
		t.Fatalf("first event = %q, want %q", first.EventName, EventDNSSnapshot)
	}
	if got := first.Attrs["tailscale.snapshot.kind"]; got != "dns" {
		t.Errorf("snapshot kind = %q, want dns", got)
	}
	if got := first.Attrs["tailscale.snapshot.reason"]; got != "change" {
		t.Errorf("first snapshot reason = %q, want change", got)
	}
	if got := first.Attrs["tailscale.snapshot.seq"]; got != "1" {
		t.Errorf("first snapshot seq = %q, want 1", got)
	}
	if got := first.Attrs["tailscale.snapshot.total"]; got != "1" {
		t.Errorf("first snapshot total = %q, want 1", got)
	}
	if !strings.Contains(first.Body, `"nameservers"`) ||
		!strings.Contains(first.Body, `"splitDNS"`) ||
		!strings.Contains(first.Body, `"preferences"`) {
		t.Fatalf("snapshot body = %q, want the wire-shaped DNS configuration", first.Body)
	}

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
	if logs[1].Attrs["tailscale.snapshot.emission_id"] == logs[0].Attrs["tailscale.snapshot.emission_id"] {
		t.Error("heartbeat reused the first emission id")
	}

	api.cfg = &tsapi.DNSConfig{Nameservers: []tsapi.DNSResolver{{Address: "192.0.2.55"}}}
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
	if err := New(&fakeAPI{cfg: &tsapi.DNSConfig{}}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if logs := rec.LogRecords(); len(logs) != 0 {
		t.Fatalf("snapshot logs = %d, want 0 when opt-in is absent", len(logs))
	}
}
