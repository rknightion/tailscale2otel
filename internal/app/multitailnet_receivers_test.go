package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/provider"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

func TestMultiTailnetReceiversRouteToMatchingRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.Cardinality.Flow.MetricsMode = "all"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Listen = "127.0.0.1:0"
	cfg.Streaming.Routes = []config.StreamingRoute{
		{Tailnet: "acme.example.com", Path: "/hec/acme", Token: "token-a"},
		{Tailnet: "beta.example.com", Path: "/hec/beta", Token: "token-b"},
	}
	cfg.Webhook.Enabled = true
	cfg.Webhook.Listen = "127.0.0.1:0"
	cfg.Webhook.Tolerance = 0
	cfg.Webhook.Routes = []config.WebhookRoute{
		{Tailnet: "acme.example.com", Secret: "secret-a"},
		{Tailnet: "beta.example.com", Secret: "secret-b"},
	}

	recA, recB := telemetrytest.New(), telemetrytest.New()
	a := newMultiReceiverTestApp(t, cfg, recA, recB)
	a.buildReceivers()
	if a.streamSrv == nil || a.webhookSrv == nil {
		t.Fatal("multi-tailnet receivers were not built")
	}

	hec := `{"event":{"nodeId":"n-beta","start":"2026-07-26T10:00:00Z","end":"2026-07-26T10:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":10,"rxBytes":20}]}}`
	req := httptest.NewRequest(http.MethodPost, "/hec/beta", strings.NewReader(hec))
	req.SetBasicAuth("", "token-b")
	w := httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("beta HEC status = %d, want 200", w.Code)
	}
	if got := metricTotal(recB, flowlog.MetricIO); got == 0 {
		t.Fatal("beta HEC emitted no flow telemetry to beta recorder")
	}
	if got := metricTotal(recA, flowlog.MetricIO); got != 0 {
		t.Fatalf("beta HEC leaked %v flow telemetry into acme recorder", got)
	}

	body := `[{"timestamp":"2026-07-26T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"beta.example.com","message":"beta","data":{"nodeID":"n-beta"}}]`
	req = httptest.NewRequest(http.MethodPost, "/tailscale/webhook", strings.NewReader(body))
	req.Header.Set("Tailscale-Webhook-Signature", webhookSignature("secret-b", body, time.Unix(1785060000, 0)))
	w = httptest.NewRecorder()
	a.webhookSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("beta webhook status = %d, want 200", w.Code)
	}
	if !hasEvent(recB, "tailscale.webhook.nodeCreated") {
		t.Fatal("beta webhook did not emit into beta recorder")
	}
	if hasEvent(recA, "tailscale.webhook.nodeCreated") {
		t.Fatal("beta webhook leaked into acme recorder")
	}
}

func TestMultiTailnetRunReportsWebhookCrossDedupPerRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Enabled = false
	cfg.SelfObservability.Enabled = true
	cfg.Collectors.Auditlogs.DedupCapacity = 1
	cfg.Webhook.Enabled = true
	cfg.Webhook.Listen = "127.0.0.1:0"
	cfg.Webhook.DedupAuditEvents = true
	cfg.Webhook.Routes = []config.WebhookRoute{
		{Tailnet: "acme.example.com", Secret: "secret-a"},
		{Tailnet: "beta.example.com", Secret: "secret-b"},
	}

	recA, recB := telemetrytest.New(), telemetrytest.New()
	a := newMultiReceiverTestApp(t, cfg, recA, recB)
	for _, set := range a.webhookDedups {
		set.Add("first")
		set.Add("second")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run() returned %v on graceful shutdown, want nil", err)
	}

	for tailnet, rec := range map[string]*telemetrytest.Recorder{
		"acme.example.com": recA,
		"beta.example.com": recB,
	} {
		for _, metric := range []string{
			"tailscale2otel.dedup.size",
			"tailscale2otel.dedup.overlap_horizon",
			"tailscale2otel.dedup.youngest_eviction_age",
		} {
			if !hasPointForSet(rec, metric, "webhook_cross") {
				t.Errorf("%s: %s{dedup.set=webhook_cross} not emitted", tailnet, metric)
			}
		}
	}
}

func TestMultiTailnetReceiverRejectsWithoutFirstRuntimeFallback(t *testing.T) {
	cfg := config.Default()
	cfg.Streaming.Enabled = true
	cfg.Streaming.Listen = "127.0.0.1:0"
	cfg.Streaming.Routes = []config.StreamingRoute{{Tailnet: "acme.example.com", Path: "/hec/acme", Token: "token-a"}, {Tailnet: "beta.example.com", Path: "/hec/beta", Token: "token-b"}}
	cfg.Webhook.Enabled = true
	cfg.Webhook.Listen = "127.0.0.1:0"
	cfg.Webhook.Tolerance = 0
	cfg.Webhook.Routes = []config.WebhookRoute{{Tailnet: "acme.example.com", Secret: "secret-a"}, {Tailnet: "beta.example.com", Secret: "secret-b"}}
	recA, recB := telemetrytest.New(), telemetrytest.New()
	a := newMultiReceiverTestApp(t, cfg, recA, recB)
	a.buildReceivers()

	req := httptest.NewRequest(http.MethodPost, "/not-a-route", strings.NewReader(`{}`))
	req.SetBasicAuth("", "token-a")
	w := httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown HEC path = %d, want 404", w.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/hec/beta", strings.NewReader(`{"event":{"nodeId":"n","start":"2026-07-26T10:00:00Z","end":"2026-07-26T10:00:01Z"}}`))
	req.SetBasicAuth("", "wrong-token")
	w = httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong HEC token = %d, want 401", w.Code)
	}
	if metricTotal(recA, flowlog.MetricIO) != 0 || metricTotal(recB, flowlog.MetricIO) != 0 {
		t.Fatal("wrong HEC token must not process records in either runtime")
	}

	body := `[{"timestamp":"2026-07-26T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"unknown.example.com","message":"unknown"}]`
	req = httptest.NewRequest(http.MethodPost, "/tailscale/webhook", strings.NewReader(body))
	req.Header.Set("Tailscale-Webhook-Signature", webhookSignature("secret-a", body, time.Unix(1785060000, 0)))
	w = httptest.NewRecorder()
	a.webhookSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unknown webhook tailnet = %d, want 401", w.Code)
	}

	mixed := `[{"timestamp":"2026-07-26T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"acme.example.com","message":"a"},{"timestamp":"2026-07-26T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"beta.example.com","message":"b"}]`
	req = httptest.NewRequest(http.MethodPost, "/tailscale/webhook", strings.NewReader(mixed))
	w = httptest.NewRecorder()
	a.webhookSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("mixed webhook tailnets = %d, want 401", w.Code)
	}
	if len(recA.LogRecords()) != 0 || len(recB.LogRecords()) != 0 || len(recA.MetricPoints("tailscale.webhook.rejected")) != 0 || len(recB.MetricPoints("tailscale.webhook.rejected")) != 0 {
		t.Fatal("unroutable requests must have zero runtime telemetry side effects")
	}
}

func TestAutoConfigureStreamingRoutesUseMatchingRuntimeClient(t *testing.T) {
	var mu sync.Mutex
	got := map[string][]tsapi.LogStreamConfig{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sink tsapi.LogStreamConfig
		if err := json.NewDecoder(r.Body).Decode(&sink); err != nil {
			t.Fatalf("decode sink: %v", err)
		}
		parts := strings.Split(r.URL.Path, "/")
		for i, part := range parts {
			if part == "tailnet" && i+1 < len(parts) {
				mu.Lock()
				got[parts[i+1]] = append(got[parts[i+1]], sink)
				mu.Unlock()
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	cfg := config.Default()
	cfg.Streaming.Enabled = true
	cfg.Streaming.Routes = []config.StreamingRoute{
		{Tailnet: "acme.example.com", Path: "/hec/acme", Token: "token-a", PublicURL: "https://recv.example/hec/acme", AutoConfigure: true},
		{Tailnet: "beta.example.com", Path: "/hec/beta", Token: "token-b", PublicURL: "https://recv.example/hec/beta", AutoConfigure: true},
	}
	a := newAppShell(cfg, "vtest", nil, telemetrytest.New().Emitter(), nil, func(_ context.Context) error { return nil }, collector.NewMemoryStore())
	a.buildProcessDeps()
	a.addRuntime("acme.example.com", telemetrytest.New().Emitter(), nil, nil, provider.Tailscale(routeTestClient(t, api.URL, "acme.example.com")), true)
	a.addRuntime("beta.example.com", telemetrytest.New().Emitter(), nil, nil, provider.Tailscale(routeTestClient(t, api.URL, "beta.example.com")), true)
	a.autoConfigureStreaming(context.Background())

	mu.Lock()
	defer mu.Unlock()
	for tailnet, wantURL := range map[string]string{"acme.example.com": "https://recv.example/hec/acme", "beta.example.com": "https://recv.example/hec/beta"} {
		sinks := got[tailnet]
		if len(sinks) != 2 {
			t.Fatalf("%s configured %d sinks, want network + configuration", tailnet, len(sinks))
		}
		for _, sink := range sinks {
			if sink.URL != wantURL {
				t.Fatalf("%s sink URL = %q, want %q", tailnet, sink.URL, wantURL)
			}
		}
	}
}

func newMultiReceiverTestApp(t *testing.T, cfg *config.Config, recA, recB *telemetrytest.Recorder) *App {
	t.Helper()
	a := newAppShell(cfg, "vtest", nil, telemetrytest.New().Emitter(), nil, func(_ context.Context) error { return nil }, collector.NewMemoryStore())
	a.buildProcessDeps()
	a.addRuntime("acme.example.com", recA.Emitter(), nil, nil, provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	a.addRuntime("beta.example.com", recB.Emitter(), nil, nil, provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	return a
}

func metricTotal(rec *telemetrytest.Recorder, name string) float64 {
	var total float64
	for _, point := range rec.MetricPoints(name) {
		total += point.Value
	}
	return total
}

func hasEvent(rec *telemetrytest.Recorder, name string) bool {
	for _, event := range rec.LogRecords() {
		if event.EventName == name {
			return true
		}
	}
	return false
}

func webhookSignature(secret, body string, timestamp time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", timestamp.Unix(), body)
	return "t=" + fmt.Sprint(timestamp.Unix()) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func routeTestClient(t *testing.T, baseURL, tailnet string) *tsapi.Client {
	t.Helper()
	client, err := tsapi.NewClient(tsapi.Options{Tailnet: tailnet, BaseURL: baseURL, APIKey: "tskey-test"})
	if err != nil {
		t.Fatalf("tsapi.NewClient(%s): %v", tailnet, err)
	}
	return client
}
