package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// TestApiObserver_RecordsRequestsAndRetries verifies the self-observability
// observer emits one api.requests point per call and api.retries only when a
// request was retried.
func TestApiObserver_RecordsRequestsAndRetries(t *testing.T) {
	rec := telemetrytest.New()
	obs := apiObserver(rec.Emitter())

	obs("devices", 200, 1)         // first-try success: no retries
	obs("logging/network", 200, 3) // succeeded after 2 retries

	reqs := rec.MetricPoints(metricAPIRequests)
	if len(reqs) != 2 {
		t.Fatalf("api.requests points = %d, want 2", len(reqs))
	}
	retries := rec.MetricPoints(metricAPIRetries)
	if len(retries) != 1 {
		t.Fatalf("api.retries points = %d, want 1 (only the retried request)", len(retries))
	}
	if retries[0].Value != 2 {
		t.Fatalf("api.retries value = %v, want 2 (attempts-1)", retries[0].Value)
	}
	if retries[0].Attrs["endpoint"] != "logging/network" {
		t.Fatalf("api.retries endpoint = %q, want logging/network", retries[0].Attrs["endpoint"])
	}
}

// TestAdminServer_Healthz verifies the admin server answers liveness checks.
func TestAdminServer_Healthz(t *testing.T) {
	srv := newAdminServer(":0")
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
		if w.Body.String() != "ok" {
			t.Fatalf("GET %s body = %q, want ok", path, w.Body.String())
		}
	}
}

// stubProvider satisfies the dependencies newApp needs in tests: an in-memory
// emitter and a no-op shutdown.
func newTestClient(t *testing.T, baseURL string) *tsapi.Client {
	t.Helper()
	c, err := tsapi.NewClient(tsapi.Options{
		Tailnet: "example.com",
		BaseURL: baseURL,
		APIKey:  "tskey-test",
	})
	if err != nil {
		t.Fatalf("tsapi.NewClient: %v", err)
	}
	return c
}

// hasCollector reports whether a collector with the given Name() is registered.
func hasCollector(a *App, name string) bool {
	for _, e := range a.registry.Entries() {
		if e.Collector.Name() == name {
			return true
		}
	}
	return false
}

// baseTestApp builds an App via the newApp seam with an in-memory emitter and a
// stub client pointed at baseURL, using the supplied (already-validated) config.
func baseTestApp(t *testing.T, cfg *config.Config, baseURL string, rec *telemetrytest.Recorder) *App {
	t.Helper()
	return newApp(cfg, "vtest", nil, rec.Emitter(), func(context.Context) error { return nil },
		newTestClient(t, baseURL), collector.NewMemoryStore())
}

// TestRegisterCollectors_NodeMetricsGating verifies the node-metrics scraper is
// registered only when enabled AND at least one target is configured.
func TestRegisterCollectors_NodeMetricsGating(t *testing.T) {
	t.Run("enabled with target -> registered", func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "example.com"
		cfg.Collectors.NodeMetrics.Enabled = true
		cfg.Collectors.NodeMetrics.Targets = []config.NodeMetricsTarget{{URL: "http://node:5252/metrics"}}
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		if !hasCollector(a, "nodemetrics") {
			t.Fatal("nodemetrics not registered when enabled with a target")
		}
	})
	t.Run("enabled without targets -> not registered", func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "example.com"
		cfg.Collectors.NodeMetrics.Enabled = true
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		if hasCollector(a, "nodemetrics") {
			t.Fatal("nodemetrics registered with no targets")
		}
	})
	t.Run("disabled -> not registered", func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "example.com"
		cfg.Collectors.NodeMetrics.Targets = []config.NodeMetricsTarget{{URL: "http://node:5252/metrics"}}
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		if hasCollector(a, "nodemetrics") {
			t.Fatal("nodemetrics registered while disabled")
		}
	})
}

// TestAutoConfigureStreaming_RegistersBothSinks verifies the gated auto_configure
// path PUTs this receiver as a Splunk-HEC sink for both log types.
func TestAutoConfigureStreaming_RegistersBothSinks(t *testing.T) {
	var mu sync.Mutex
	got := map[string]tsapi.LogStreamConfig{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		var logType string
		for i, p := range parts {
			if p == "logging" && i+1 < len(parts) {
				logType = parts[i+1]
			}
		}
		var cfg tsapi.LogStreamConfig
		_ = json.NewDecoder(r.Body).Decode(&cfg)
		mu.Lock()
		got[logType] = cfg
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Tailscale.Auth.Method = "apikey"
	cfg.Tailscale.Auth.APIKey = "tskey-test"
	cfg.Streaming.Enabled = true
	cfg.Streaming.AutoConfigure = true
	cfg.Streaming.PublicURL = "https://recv.example/services/collector/event"
	cfg.Streaming.Token = "hectoken"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config should be valid: %v", err)
	}

	a := baseTestApp(t, cfg, srv.URL, telemetrytest.New())
	a.autoConfigureStreaming(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("registered %d sinks, want 2 (network+configuration)", len(got))
	}
	for _, lt := range []string{"network", "configuration"} {
		c, ok := got[lt]
		if !ok {
			t.Fatalf("no sink registered for %q", lt)
		}
		if c.DestinationType != "splunk" || c.URL != "https://recv.example/services/collector/event" || c.Token != "hectoken" {
			t.Fatalf("%s sink = %+v", lt, c)
		}
	}
}

// TestNewApp_WiresSharedAuditDedup verifies the app gives audit.Processor a shared
// dedup set so a duplicate event (same identity) is counted once.
func TestNewApp_WiresSharedAuditDedup(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)

	ev := audit.Event{
		EventGroupID: "g1",
		EventTime:    time.Unix(1700000000, 0).UTC(),
		Action:       "CREATE",
		Actor:        audit.Actor{LoginName: "a@example.com"},
		Target:       audit.Target{Type: "NODE", Name: "n", ID: "t1"},
	}
	a.auditProc.Process(ev, rec.Emitter())
	a.auditProc.Process(ev, rec.Emitter()) // exact duplicate

	var total float64
	for _, p := range rec.MetricPoints("tailscale.config.audit.events") {
		total += p.Value
	}
	if total != 1 {
		t.Fatalf("audit events counter = %v, want 1 (shared dedup wired)", total)
	}
}

// TestNewApp_WiresSharedFlowDedup verifies the app gives flowlog.Processor a shared
// dedup set so a duplicate flow window is processed once.
func TestNewApp_WiresSharedFlowDedup(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)

	flow := flowlog.FlowLog{
		NodeID: "n-x",
		Start:  time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC),
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: 6, Src: "100.64.0.1:1", Dst: "100.64.0.2:443", TxBytes: 1000, RxBytes: 500},
		},
	}
	sum := func() float64 {
		var t float64
		for _, p := range rec.MetricPoints(flowlog.MetricIO) {
			t += p.Value
		}
		return t
	}
	a.flowProc.Process(flow, rec.Emitter())
	first := sum()
	a.flowProc.Process(flow, rec.Emitter()) // duplicate window
	if first == 0 {
		t.Fatal("flow io total = 0 after first process, want >0")
	}
	if second := sum(); second != first {
		t.Fatalf("flow io total after duplicate = %v, want %v (shared dedup wired)", second, first)
	}
}

// TestApp_RunGracefulShutdown is the app-level integration test (P1-5): assemble
// an App via the newApp seam with an in-memory emitter and a stub Tailscale
// server, run it briefly, and confirm a cancelled context produces a CLEAN
// (nil) return plus the heartbeat and build_info self-observability signals.
func TestApp_RunGracefulShutdown(t *testing.T) {
	// A stub Tailscale API that 200s everything (collectors won't tick within the
	// short run window, but this keeps any stray call harmless).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	if _, err := url.Parse(ts.URL); err != nil {
		t.Fatalf("bad stub url: %v", err)
	}

	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Tailscale.Auth.Method = "apikey"
	cfg.Tailscale.Auth.APIKey = "tskey-test"
	cfg.SelfObservability.Enabled = true

	rec := telemetrytest.New()
	var shutdownCalled bool
	shutdown := func(context.Context) error { shutdownCalled = true; return nil }

	a := newApp(cfg, "v9.9.9", nil, rec.Emitter(), shutdown,
		newTestClient(t, ts.URL), collector.NewMemoryStore())

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run() returned %v on graceful shutdown, want nil", err)
	}
	if !shutdownCalled {
		t.Fatal("telemetry shutdown was not invoked")
	}

	// The heartbeat must have emitted at least once...
	if pts := rec.MetricPoints("tailscale2otel.up"); len(pts) == 0 || pts[0].Value != 1 {
		t.Fatalf("tailscale2otel.up = %+v, want a point of value 1", pts)
	}
	// ...and build_info must carry the version we passed.
	bi := rec.MetricPoints("tailscale2otel.build_info")
	if len(bi) != 1 {
		t.Fatalf("build_info points = %d, want 1", len(bi))
	}
	if bi[0].Attrs["service.version"] != "v9.9.9" {
		t.Fatalf("build_info service.version = %q, want v9.9.9", bi[0].Attrs["service.version"])
	}
}
