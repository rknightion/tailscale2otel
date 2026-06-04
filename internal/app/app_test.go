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

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
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

	reqs := rec.MetricPoints(appcatalog.MetricAPIRequests)
	if len(reqs) != 2 {
		t.Fatalf("api.requests points = %d, want 2", len(reqs))
	}
	retries := rec.MetricPoints(appcatalog.MetricAPIRetries)
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

// TestNewApp_ReverseDNSGating verifies the async reverse-DNS cache is constructed
// (and wired into the flow processor) only when enrichment.reverse_dns is enabled.
func TestNewApp_ReverseDNSGating(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	if a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New()); a.rdnsCache != nil {
		t.Fatal("rdnsCache should be nil when reverse_dns is disabled")
	}

	cfg2 := config.Default()
	cfg2.Tailscale.Tailnet = "example.com"
	cfg2.Enrichment.ReverseDNS.Enabled = true
	a := baseTestApp(t, cfg2, "http://127.0.0.1:0", telemetrytest.New())
	if a.rdnsCache == nil {
		t.Fatal("rdnsCache should be non-nil when reverse_dns is enabled")
	}
	a.rdnsCache.Close()
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
	t.Run("enabled, no targets, discovery enabled -> registered", func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "example.com"
		cfg.Collectors.NodeMetrics.Enabled = true
		cfg.Collectors.NodeMetrics.Discovery.Enabled = true
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		if !hasCollector(a, "nodemetrics") {
			t.Fatal("nodemetrics not registered when enabled with discovery and no static targets")
		}
	})
}

// TestRegisterCollectors_FeatureProbeStreamMode verifies the flowlogs feature
// probe is registered exactly when flowlogs is enabled but NOT polling (stream
// mode), so tailscale.feature.enabled keeps being emitted without the poller. In
// poll mode the poller emits the gauge itself, so no probe is registered.
func TestRegisterCollectors_FeatureProbeStreamMode(t *testing.T) {
	t.Run("stream source -> probe registered, poller not", func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "example.com"
		cfg.Collectors.Flowlogs.Enabled = true
		cfg.Collectors.Flowlogs.Source = "stream"
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		if !hasCollector(a, "flowlogs-feature") {
			t.Fatal("flowlogs-feature probe not registered in stream mode")
		}
		if hasCollector(a, "flowlogs") {
			t.Fatal("flowlogs poller registered in stream mode")
		}
	})
	t.Run("poll source -> poller registered, no probe", func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "example.com"
		cfg.Collectors.Flowlogs.Enabled = true
		cfg.Collectors.Flowlogs.Source = "poll"
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		if !hasCollector(a, "flowlogs") {
			t.Fatal("flowlogs poller not registered in poll mode")
		}
		if hasCollector(a, "flowlogs-feature") {
			t.Fatal("feature probe registered in poll mode (the poller already emits it)")
		}
	})
	t.Run("flowlogs disabled -> neither", func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "example.com"
		cfg.Collectors.Flowlogs.Enabled = false
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		if hasCollector(a, "flowlogs-feature") || hasCollector(a, "flowlogs") {
			t.Fatal("flowlogs collectors registered while disabled")
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

// postStream sends body through the app's stream receiver Handler, which shares
// the flow/audit processors and their dedup set with the poll path, and returns
// the HTTP status. It is the in-process equivalent of a Tailscale log-stream POST.
func postStream(t *testing.T, a *App, body string) int {
	t.Helper()
	if a.streamSrv == nil {
		t.Fatal("stream server not built (streaming.enabled?)")
	}
	req := httptest.NewRequest(http.MethodPost, "/services/collector/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(w, req)
	return w.Code
}

// TestPollStreamCrossDedup_FlowDeduplicates pins S4-9(2) for FLOW logs: a
// connection reported by BOTH the poll collector and the stream receiver is
// counted ONCE. The flow dedup key is content-based (nodeId|start|end|proto|src|dst),
// byte-identical across the two sources, so the shared set suppresses the second
// copy. (Verified live against the example-tailnet lab on 2026-06-03: an identical flow
// replayed through the receiver did not double-count.)
func TestPollStreamCrossDedup_FlowDeduplicates(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Path = "/services/collector/event"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)

	sum := func() float64 {
		var tot float64
		for _, p := range rec.MetricPoints(flowlog.MetricIO) {
			tot += p.Value
		}
		return tot
	}

	// POLL side: the collector processes the flow window.
	pollFlow := flowlog.FlowLog{
		NodeID: "n-x",
		Start:  time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 6, 3, 10, 0, 5, 0, time.UTC),
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: 6, Src: "100.64.0.1:1", Dst: "100.64.0.2:443", TxBytes: 1000, RxBytes: 500},
		},
	}
	a.flowProc.Process(pollFlow, rec.Emitter())
	afterPoll := sum()
	if afterPoll == 0 {
		t.Fatal("flow io total = 0 after poll, want > 0")
	}

	// STREAM side: the SAME connection arrives via the receiver (HEC-wrapped). Its
	// content key matches the poll copy, so it must be suppressed (no double-count).
	streamBody := `{"time":1780480800.0,"event":{"nodeId":"n-x","start":"2026-06-03T10:00:00Z","end":"2026-06-03T10:00:05Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":1000,"rxBytes":500}]},"fields":{"recorded":"2026-06-03T10:00:06Z"}}`
	if code := postStream(t, a, streamBody); code != http.StatusOK {
		t.Fatalf("stream POST status = %d, want 200", code)
	}
	if after := sum(); after != afterPoll {
		t.Fatalf("flow io total after stream copy = %v, want %v (cross-source dedup must suppress the duplicate)", after, afterPoll)
	}
}

// TestPollStreamCrossDedup_AuditDeduplicates pins S4-9(2) for CONFIG audit events.
// A streamed audit record carries no inner eventTime (it is timed from the ms HEC
// envelope), while the poll collector has the API's ns eventTime — so a timestamp
// component never matches across the two sources. The audit dedup key therefore
// uses the SOURCE-INDEPENDENT identity eventGroupID|action|target.id|property
// (time-free), so a change reported by BOTH paths is counted ONCE. This is a
// best-effort FAILSAFE for the discouraged dual-ingestion config (source=both):
// the supported setup is to pick ONE method per log type. (S4-9(2), verified live
// against example-tailnet 2026-06-03; key change is the session-6 fix.)
func TestPollStreamCrossDedup_AuditDeduplicates(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Path = "/services/collector/event"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)

	count := func() float64 {
		var tot float64
		for _, p := range rec.MetricPoints("tailscale.config.audit.events") {
			tot += p.Value
		}
		return tot
	}

	// POLL side: the API event carries a nanosecond eventTime.
	pollEv := audit.Event{
		EventGroupID: "egA",
		EventTime:    time.Unix(1780486201, 434327047).UTC(), // 2026-06-03T11:30:01.434327047Z
		Action:       "DELETE",
		Target:       audit.Target{ID: "t1", Type: "NODE"},
		Actor:        audit.Actor{ID: "a1"},
	}
	a.auditProc.Process(pollEv, rec.Emitter())
	if c := count(); c != 1 {
		t.Fatalf("audit count after poll = %v, want 1", c)
	}

	// STREAM side: the SAME change, with NO inner eventTime and a ms HEC time that
	// does NOT equal the poll eventTime. Because the key is time-free, the keys
	// match across sources and the duplicate is suppressed (counted once).
	streamBody := `{"time":1780486201.434,"event":{"eventGroupID":"egA","origin":"NODE","action":"DELETE","target":{"id":"t1","type":"NODE"},"actor":{"id":"a1"}},"fields":{"recorded":"2026-06-03T11:30:05Z"}}`
	if code := postStream(t, a, streamBody); code != http.StatusOK {
		t.Fatalf("stream POST status = %d, want 200", code)
	}
	if c := count(); c != 1 {
		t.Fatalf("audit count after stream copy = %v, want 1 (cross-source dedup must suppress the duplicate)", c)
	}
}

// TestPollStreamCrossDedup_AuditDistinctSubChangesBothEmit guards D11: the
// time-free composite key must NOT over-suppress distinct sub-changes that share
// an eventGroupID. Real audit data shows one eventGroupID spanning several events
// (e.g. UPDATE MACHINE_NAME, UPDATE ACL_TAGS) at sub-millisecond spacing — these
// differ in (action,target,property) and must each be counted.
func TestPollStreamCrossDedup_AuditDistinctSubChangesBothEmit(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)

	count := func() float64 {
		var tot float64
		for _, p := range rec.MetricPoints("tailscale.config.audit.events") {
			tot += p.Value
		}
		return tot
	}
	base := audit.Event{EventGroupID: "egG", EventTime: time.Unix(1780486201, 0).UTC(), Action: "UPDATE", Target: audit.Target{ID: "t1", Type: "NODE"}}
	a1 := base
	a1.Target.Property = "MACHINE_NAME"
	a2 := base
	a2.Target.Property = "ACL_TAGS"
	a.auditProc.Process(a1, rec.Emitter())
	a.auditProc.Process(a2, rec.Emitter())
	if c := count(); c != 2 {
		t.Fatalf("audit count = %v, want 2 (distinct properties under one eventGroupID must not collapse)", c)
	}
}

// TestNewApp_WiresWebhookAuditCrossDedup verifies that with
// webhook.dedup_audit_events on, the app shares ONE cross-source dedup set
// between the audit processor and the webhook server, so a change reported by
// both is counted once (the webhook copy is suppressed).
func TestNewApp_WiresWebhookAuditCrossDedup(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Webhook.Enabled = true
	cfg.Webhook.Path = "/webhook"
	cfg.Webhook.DedupAuditEvents = true
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)

	if a.webhookSrv == nil {
		t.Fatal("webhook server not built")
	}
	if a.webhookDedup == nil {
		t.Fatal("cross-source dedup set not wired with dedup_audit_events on")
	}

	// Audit records the NODE/CREATE/n1 change at the matching second.
	a.auditProc.Process(audit.Event{
		EventTime: time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
		Action:    "CREATE",
		Target:    audit.Target{ID: "n1", Type: "NODE"},
		Actor:     audit.Actor{LoginName: "a@example.com"},
	}, rec.Emitter())

	// A webhook for the SAME change must be suppressed by the shared set.
	body := `[{"timestamp":"2024-06-06T15:25:26Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"m","data":{"nodeID":"n1"}}]`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.webhookSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook POST status = %d, want 200", w.Code)
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.webhook.nodeCreated" {
			t.Fatal("webhook nodeCreated was NOT suppressed by cross-source dedup")
		}
	}
}

// TestNewApp_WebhookCrossDedupOffByDefault verifies the cross-source set is NOT
// wired without the flag, so both sources emit (back-compat).
func TestNewApp_WebhookCrossDedupOffByDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Webhook.Enabled = true
	cfg.Webhook.Path = "/webhook"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)
	if a.webhookDedup != nil {
		t.Fatal("cross-source dedup set wired without the flag")
	}

	a.auditProc.Process(audit.Event{
		EventTime: time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
		Action:    "CREATE",
		Target:    audit.Target{ID: "n1", Type: "NODE"},
		Actor:     audit.Actor{LoginName: "a@example.com"},
	}, rec.Emitter())

	body := `[{"timestamp":"2024-06-06T15:25:26Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"m","data":{"nodeID":"n1"}}]`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.webhookSrv.Handler().ServeHTTP(w, req)

	var found bool
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.webhook.nodeCreated" {
			found = true
		}
	}
	if !found {
		t.Fatal("webhook nodeCreated suppressed despite dedup_audit_events off")
	}
}

// TestApp_RunGracefulShutdown is the app-level integration test (P1-5): assemble
// an App via the newApp seam with an in-memory emitter and a stub Tailscale
// server, run it briefly, and confirm a canceled context produces a CLEAN
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

	// The runtime reporter must have been started by Run (it emits immediately).
	if pts := rec.MetricPoints("tailscale2otel.runtime.goroutines"); len(pts) == 0 {
		t.Fatal("tailscale2otel.runtime.goroutines not emitted; runtime reporter not wired into Run")
	}
	// The dedup reporter must have been started by Run and reported the flow set.
	if !hasPointForSet(rec, "tailscale2otel.dedup.size", "flow") {
		t.Fatal("tailscale2otel.dedup.size{flow} not emitted; dedup reporter not wired into Run")
	}
}
