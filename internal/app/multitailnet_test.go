package app

import (
	"context"
	"testing"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/provider"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// twoTailnetApp builds an App fanned out over two tailnets ("acme"/"beta"), each
// with its own injected emitter + stub Tailscale provider. It exercises the
// addRuntime fan-out path used by New() without touching the network.
func twoTailnetApp(t *testing.T) (*App, *telemetrytest.Recorder, *telemetrytest.Recorder) {
	t.Helper()
	cfg := config.Default()
	recA, recB := telemetrytest.New(), telemetrytest.New()
	a := newAppShell(cfg, "vtest", nil, telemetrytest.New().Emitter(),
		tracenoop.NewTracerProvider().Tracer("test"),
		func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.buildProcessDeps()
	a.addRuntime("acme.example.com", recA.Emitter(), nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	a.addRuntime("beta.example.com", recB.Emitter(), nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	return a, recA, recB
}

func TestMultiTailnet_FansOutRuntimes(t *testing.T) {
	a, recA, recB := twoTailnetApp(t)

	if len(a.runtimes) != 2 {
		t.Fatalf("runtimes = %d, want 2", len(a.runtimes))
	}
	if a.runtimes[0].emitter != recA.Emitter() || a.runtimes[1].emitter != recB.Emitter() {
		t.Fatal("each runtime must hold its own injected emitter")
	}
	if a.runtimes[0].registry == a.runtimes[1].registry {
		t.Fatal("each runtime must have its own registry")
	}
	if a.runtimes[0].cache == a.runtimes[1].cache {
		t.Fatal("each runtime must have its own enrichment cache")
	}
	// Devices is enabled by Default(), so each runtime registers it independently.
	if !runtimeHasCollector(a.runtimes[0], "devices") || !runtimeHasCollector(a.runtimes[1], "devices") {
		t.Fatal("both runtimes should register the devices collector")
	}
}

func TestMultiTailnet_CheckpointNamespacing(t *testing.T) {
	a, _, _ := twoTailnetApp(t)
	if got := a.checkpointKeyFor(a.runtimes[0], "flowlogs"); got != "acme.example.com/flowlogs" {
		t.Errorf("checkpointKeyFor = %q, want acme.example.com/flowlogs", got)
	}
	if got := a.checkpointKeyFor(a.runtimes[1], "auditlogs"); got != "beta.example.com/auditlogs" {
		t.Errorf("checkpointKeyFor = %q, want beta.example.com/auditlogs", got)
	}
}

func TestMultiTailnet_StatusHasPerTailnetSections(t *testing.T) {
	a, _, _ := twoTailnetApp(t)
	s := a.buildStatus()
	if len(s.Tailnets) != 2 {
		t.Fatalf("status Tailnets = %d, want 2", len(s.Tailnets))
	}
	names := map[string]bool{s.Tailnets[0].Name: true, s.Tailnets[1].Name: true}
	if !names["acme.example.com"] || !names["beta.example.com"] {
		t.Errorf("tailnet section names = %v, want acme/beta", names)
	}
	// The combined top-level collector list is the sum of the per-tailnet lists.
	combined := len(s.Tailnets[0].Collectors) + len(s.Tailnets[1].Collectors)
	if len(s.Collectors) != combined {
		t.Errorf("combined Collectors = %d, want %d (sum of per-tailnet)", len(s.Collectors), combined)
	}
	if s.Service.Tailnet != "2 tailnets" {
		t.Errorf("Service.Tailnet = %q, want \"2 tailnets\"", s.Service.Tailnet)
	}
}

// runtimeHasCollector reports whether the runtime's registry contains a collector
// with the given name.
func runtimeHasCollector(rt *tailnetRuntime, name string) bool {
	for _, e := range rt.registry.Entries() {
		if e.Collector.Name() == name {
			return true
		}
	}
	return false
}
