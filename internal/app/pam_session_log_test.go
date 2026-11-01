package app

import (
	"context"
	"testing"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/pam"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// TestPAMSessionLogUsesSelectedRuntime verifies that the TSO-0137 session-log
// option follows the selected PAM runtime without depending on log identity
// attribute names, which are governed by the PII policy.
func TestPAMSessionLogUsesSelectedRuntime(t *testing.T) {
	for _, age := range []time.Duration{0, time.Nanosecond} {
		t.Run(age.String(), func(t *testing.T) { testPAMSessionLogUsesSelectedRuntime(t, age) })
	}
}

func testPAMSessionLogUsesSelectedRuntime(t *testing.T, age time.Duration) {
	srv := pamFixtureServer(t)
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.Delivery.LogMaxRecordAge = config.Duration(age)
	cfg.Collectors.PAM.Enabled = true
	cfg.Collectors.PAM.SessionLogEnabled = true
	cfg.PAM.Tailnet = "beta.example.invalid"
	cfg.PAM.Token = "static-token"
	cfg.PAM.APIURL = srv.URL
	client, err := b0api.NewClient(pamAPIOptions(cfg, "vtest"))
	if err != nil {
		t.Fatal(err)
	}

	recAlpha, recBeta := telemetrytest.New(), telemetrytest.New()
	a := newAppShell(cfg, "vtest", nil, telemetrytest.New().Emitter(),
		tracenoop.NewTracerProvider().Tracer("test"),
		func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.pamClient = client
	a.buildProcessDeps()
	a.addRuntime("alpha.example.invalid", tailnetRecorderEmitter{Emitter: recAlpha.Emitter(), tailnet: "alpha.example.invalid"}, nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	a.addRuntime("beta.example.invalid", tailnetRecorderEmitter{Emitter: recBeta.Emitter(), tailnet: "beta.example.invalid"}, nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	assertPAMRegistration(t, a, "beta.example.invalid")

	for _, rt := range a.runtimes {
		for _, entry := range rt.registry.Entries() {
			if entry.Collector.Name() != "pam_sessions" {
				continue
			}
			sessions, ok := entry.Collector.(collector.SnapshotCollector)
			if !ok {
				t.Fatalf("PAM session collector is not a SnapshotCollector")
			}
			// Keep the fixed historical fixture and the collection horizon on one
			// deterministic timeline, independently of the host wall clock.
			pam.WithSessionsClock(func() time.Time {
				return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			})(entry.Collector.(*pam.SessionsCollector))
			if err := sessions.Collect(context.Background(), rt.emitter); err != nil {
				t.Fatalf("collect PAM sessions on %q: %v", rt.configuredName, err)
			}
		}
	}

	wantLogs := 0
	for _, point := range recBeta.MetricPoints("tailscale.pam.sessions") {
		wantLogs += int(point.Value)
	}
	if age == 0 && wantLogs == 0 {
		t.Fatal("PAM session collector emitted no accepted-session metrics")
	}

	if age > 0 && wantLogs != 0 {
		t.Fatalf("old fixture emitted %d session deltas with horizon enabled", wantLogs)
	}

	gotLogs := 0
	for _, record := range recBeta.LogRecords() {
		if record.EventName != "tailscale.pam.session" {
			continue
		}
		gotLogs++
		if got := record.Attrs["tailscale.tailnet"]; got != "beta.example.invalid" {
			t.Errorf("session log tailnet = %q, want beta.example.invalid", got)
		}
	}
	if gotLogs != wantLogs {
		t.Errorf("session log records = %d, want one for each of %d accepted sessions", gotLogs, wantLogs)
	}
	for _, record := range recAlpha.LogRecords() {
		if record.EventName == "tailscale.pam.session" {
			t.Errorf("unselected runtime emitted a PAM session log")
		}
	}
	t.Logf("PAM session logs on beta.example.invalid: %d records for %d accepted sessions", gotLogs, wantLogs)
}
