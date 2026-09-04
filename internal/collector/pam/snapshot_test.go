package pam

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestSnapshotIsSafeChangeDrivenAndHeartbeatRefreshed(t *testing.T) {
	api := fixtureAPI(t)
	api.organization.OwnerEmail = "owner-secret@example.invalid"
	api.organization.Certificate.MTLSCertificate = "certificate-secret"
	api.connectors[0].Metadata.ConnectorInternalMetadata.IPAddress = "198.51.100.9"
	api.connectors[0].Metadata.ConnectorInternalMetadata.HostMetadata.Hostname = "connector-private-host"
	api.sockets[0].ProtectedUsername = "protected-user-secret"
	api.sockets[0].ProtectedPassword = "protected-password-secret"
	upstream := api.upstream[api.sockets[0].SocketID]
	standard := upstream[0].Config.DatabaseServiceConfiguration.StandardDatabaseServiceConfiguration
	standard.Hostname = "database-private-host"
	standard.DatabaseName = "database-private-name"
	standard.UsernameAndPasswordAuthConfiguration = &b0api.UsernameAndPasswordAuthConfiguration{
		Username: "upstream-user-secret", Password: "upstream-password-secret",
	}
	api.upstream[api.sockets[0].SocketID] = upstream

	clock := time.Date(2026, 9, 4, 20, 0, 0, 0, time.UTC)
	c := New(api, 0, WithClock(func() time.Time { return clock }), WithSnapshot(true), WithSnapshotHeartbeat(time.Hour))
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
	if logs[0].EventName != EventSnapshot || logs[0].Attrs["tailscale.snapshot.kind"] != "pam" || logs[0].Attrs["tailscale.snapshot.reason"] != "change" {
		t.Fatalf("first snapshot metadata = event %q attrs %v", logs[0].EventName, logs[0].Attrs)
	}
	for _, safe := range []string{"\"connectors\"", "\"services\"", "\"policies\"", "\"identities\"", "\"upstream_configurations\"", "\"authentication_type\""} {
		if !strings.Contains(logs[0].Body, safe) {
			t.Errorf("snapshot body missing safe shape %s: %s", safe, logs[0].Body)
		}
	}
	for _, secret := range []string{
		"owner-secret@example.invalid", "certificate-secret", "198.51.100.9", "connector-private-host",
		"protected-user-secret", "protected-password-secret", "database-private-host", "database-private-name",
		"upstream-user-secret", "upstream-password-secret", "username_and_password_auth_configuration",
		"private_key_auth_configuration", "border0_certificate_auth_configuration",
	} {
		if strings.Contains(logs[0].Body, secret) {
			t.Errorf("snapshot leaked forbidden value/field %q: %s", secret, logs[0].Body)
		}
	}

	clock = clock.Add(30 * time.Minute)
	if logs = collect("unchanged"); len(logs) != 1 {
		t.Fatalf("unchanged logs = %d, want 1 cumulative", len(logs))
	}
	clock = clock.Add(30 * time.Minute)
	logs = collect("heartbeat")
	if len(logs) != 2 || logs[1].Attrs["tailscale.snapshot.reason"] != "heartbeat" {
		t.Fatalf("heartbeat logs = %+v", logs)
	}
	api.organization.MFARequired = !api.organization.MFARequired
	clock = clock.Add(time.Minute)
	logs = collect("change")
	if len(logs) != 3 || logs[2].Attrs["tailscale.snapshot.reason"] != "change" {
		t.Fatalf("changed logs = %+v", logs)
	}
}

func TestSnapshotHonorsBodyCapAndCanBeReassembled(t *testing.T) {
	api := fixtureAPI(t)
	rec := telemetrytest.New()
	if err := New(api, 0, WithSnapshot(true, 128)).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	logs := rec.LogRecords()
	if len(logs) < 2 {
		t.Fatalf("snapshot chunks = %d, want multiple", len(logs))
	}
	sort.Slice(logs, func(i, j int) bool {
		left, _ := strconv.Atoi(logs[i].Attrs["tailscale.snapshot.seq"])
		right, _ := strconv.Atoi(logs[j].Attrs["tailscale.snapshot.seq"])
		return left < right
	})
	var body strings.Builder
	for i, log := range logs {
		if len(log.Body) > 128 {
			t.Errorf("chunk %d bytes = %d, want <=128", i+1, len(log.Body))
		}
		body.WriteString(log.Body)
	}
	if !strings.Contains(body.String(), "\"subscription_limits\"") {
		t.Fatalf("reassembled snapshot missing tail content")
	}
}

func TestSnapshotIgnoresUpstreamConfigurationOrder(t *testing.T) {
	api := fixtureAPI(t)
	serviceID := api.sockets[0].SocketID
	api.upstream[serviceID] = []b0api.UpstreamConfiguration{
		{Config: b0api.ServiceConfiguration{ServiceType: "ssh", SSHServiceConfiguration: &b0api.SSHServiceConfiguration{
			StandardSSHServiceConfiguration: &b0api.StandardSSHServiceConfiguration{Port: 22, SSHAuthenticationType: "certificate"},
		}}},
		{Config: b0api.ServiceConfiguration{ServiceType: "database", DatabaseServiceConfiguration: &b0api.DatabaseServiceConfiguration{
			DatabaseServiceType: "standard",
			StandardDatabaseServiceConfiguration: &b0api.StandardDatabaseServiceConfiguration{
				Port: 5432, Protocol: "postgres", AuthenticationType: "username_and_password",
			},
		}}},
	}
	clock := time.Date(2026, 9, 4, 20, 0, 0, 0, time.UTC)
	c := New(api, 0, WithClock(func() time.Time { return clock }), WithSnapshot(true), WithSnapshotHeartbeat(time.Hour))
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	api.upstream[serviceID][0], api.upstream[serviceID][1] = api.upstream[serviceID][1], api.upstream[serviceID][0]
	clock = clock.Add(time.Minute)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.LogRecords()); got != 1 {
		t.Fatalf("snapshot logs after upstream reorder = %d, want 1 unchanged snapshot", got)
	}
}

func TestSnapshotInitializationErrorIsReturned(t *testing.T) {
	err := New(fixtureAPI(t), 0, WithSnapshot(true, 1)).Collect(context.Background(), telemetrytest.New().Emitter())
	if err == nil {
		t.Fatal("Collect returned nil with invalid snapshot body limit")
	}
}
