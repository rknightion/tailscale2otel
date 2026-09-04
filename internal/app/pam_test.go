package app

import (
	"context"
	"testing"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestPAMAPIOptionsCarryFrozenConnectionSeam(t *testing.T) {
	cfg := config.Default()
	cfg.PAM.APIURL = "https://pam.example.invalid/api/v1"
	cfg.PAM.Token = "static-token"

	got := pamAPIOptions(cfg, "v5.0.0")
	if got.BaseURL != cfg.PAM.APIURL || got.Token != "static-token" {
		t.Fatalf("PAM options = BaseURL %q Token %q", got.BaseURL, got.Token)
	}
	if got.UserAgent != "tailscale2otel/v5.0.0" {
		t.Fatalf("PAM User-Agent = %q", got.UserAgent)
	}
}

func TestPAMCollectorsRegisterOnlyOnPrimaryRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.Collectors.PAM.Enabled = true
	cfg.PAM.Token = "static-token"
	client, err := b0api.NewClient(pamAPIOptions(cfg, "vtest"))
	if err != nil {
		t.Fatal(err)
	}
	a := newAppShell(cfg, "vtest", nil, telemetrytest.New().Emitter(),
		tracenoop.NewTracerProvider().Tracer("test"),
		func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.pamClient = client
	a.buildProcessDeps()
	a.addRuntime("alpha.example.invalid", telemetrytest.New().Emitter(), nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	a.addRuntime("beta.example.invalid", telemetrytest.New().Emitter(), nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)

	for i, rt := range a.runtimes {
		var pamInventory, pamSessions int
		for _, entry := range rt.registry.Entries() {
			switch entry.Collector.Name() {
			case "pam":
				pamInventory++
			case "pam_sessions":
				pamSessions++
			}
		}
		want := 0
		if i == 0 {
			want = 1
		}
		if pamInventory != want || pamSessions != want {
			t.Errorf("runtime %d PAM collectors = inventory %d sessions %d, want %d each", i, pamInventory, pamSessions, want)
		}
	}
}
