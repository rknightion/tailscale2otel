package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// Keep the production New/client/scheduler/receiver/export path intact, replacing
// only the network edge. The two inventories deliberately discover distinct nodes.
type runtimeTraceTransport struct{ fallback http.RoundTripper }

func (rt runtimeTraceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := ""
	switch r.URL.Hostname() {
	case "api.tailscale.com":
		if !strings.HasSuffix(r.URL.Path, "/devices") {
			return nil, fmt.Errorf("unexpected API request: %s", r.URL.Path)
		}
		addr := "100.64.0.1"
		if strings.Contains(r.URL.Path, "beta.example.com") {
			addr = "100.64.0.2"
		}
		body = fmt.Sprintf(`{"devices":[{"id":"synthetic-node","hostname":"synthetic","name":"synthetic.example.com","addresses":[%q],"connectedToControl":true}]}`, addr)
	case "100.64.0.1", "100.64.0.2":
		body = "# TYPE synthetic_node_load gauge\nsynthetic_node_load 0.5\n"
	case "api.github.com":
		body = `{"tag_name":"v5.0.0"}`
	case "pkgs.tailscale.com":
		body = `{"Version":"1.90.0"}`
	default:
		return rt.fallback.RoundTrip(r)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestRuntimeTraceExportOwnership(t *testing.T) {
	for _, reveal := range []bool{true, false} {
		t.Run(fmt.Sprintf("tailnet_visible_%t", reveal), func(t *testing.T) {
			original := http.DefaultTransport
			http.DefaultTransport = runtimeTraceTransport{fallback: original}
			defer func() { http.DefaultTransport = original }()
			var mu sync.Mutex
			var spans []*tracepb.Span
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/traces" {
					data, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read trace body: %v", err)
						w.WriteHeader(400)
						return
					}
					var req collectortrace.ExportTraceServiceRequest
					if err := proto.Unmarshal(data, &req); err != nil {
						t.Errorf("decode traces: %v", err)
						w.WriteHeader(400)
						return
					}
					mu.Lock()
					for _, rs := range req.ResourceSpans {
						for _, ss := range rs.ScopeSpans {
							spans = append(spans, ss.Spans...)
						}
					}
					mu.Unlock()
				}
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer sink.Close()
			cfg := config.Default()
			cfg.OTLP.Protocol = "http"
			cfg.OTLP.Endpoint = sink.URL
			cfg.OTLP.TLS.Insecure = true
			cfg.OTLP.Compression = "none"
			cfg.OTLP.MetricInterval = config.Duration(time.Hour)
			cfg.Checkpoint.Store = "memory"
			cfg.Checkpoint.EvidenceStore = "memory"
			cfg.Tracing.Enabled = true
			cfg.Tracing.Sampler = "always_on"
			cfg.PIIFilter.TailnetName = reveal
			cfg.Admin.Enabled = false
			node := cfg.Collectors.NodeMetrics
			c := &cfg.Collectors
			for _, enabled := range []*bool{&c.Devices.Enabled, &c.Users.Enabled, &c.Keys.Enabled, &c.Settings.Enabled, &c.Acl.Enabled, &c.Dns.Enabled, &c.Contacts.Enabled, &c.Webhooks.Enabled, &c.PostureIntegrations.Enabled, &c.LogStream.Enabled, &c.Services.Enabled, &c.OAuthApps.Enabled, &c.Flowlogs.Enabled, &c.Auditlogs.Enabled} {
				*enabled = false
			}
			node.Enabled = true
			node.Interval = config.Duration(time.Hour)
			node.Discovery.Enabled = true
			cfg.Collectors.NodeMetrics = node
			cfg.Scheduler.InitialStaggerWindow = 0
			names := []string{"alpha.example.com", "beta.example.com"}
			for i, name := range names {
				cfg.Tailnets = append(cfg.Tailnets, config.TailnetConfig{Name: name, Auth: config.TailscaleAuth{Method: "apikey", APIKey: "synthetic-key"}})
				cfg.Streaming.Routes = append(cfg.Streaming.Routes, config.StreamingRoute{Tailnet: name, Path: fmt.Sprintf("/hec/%d", i), Token: "synthetic-token"})
				cfg.Webhook.Routes = append(cfg.Webhook.Routes, config.WebhookRoute{Tailnet: name, Secret: "synthetic-secret"})
			}
			cfg.Streaming.Enabled = true
			cfg.Streaming.Listen = "127.0.0.1:0"
			cfg.Webhook.Enabled = true
			cfg.Webhook.Listen = "127.0.0.1:0"
			cfg.Webhook.Tolerance = 0
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			a, err := New(context.Background(), cfg, "v-test", discardLog())
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			defer func() {
				if !closed {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = a.Close(ctx)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, len(a.runtimes))
			for _, rt := range a.runtimes {
				go func() { done <- rt.sched.Run(ctx, rt.registry) }()
			}
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				ready := true
				for _, rt := range a.runtimes {
					if !rt.status.Snapshot()["nodemetrics"].LastSuccess {
						ready = false
					}
				}
				if ready {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("scheduler did not complete: %v", a.collectorStatuses(time.Now()))
				case <-ticker.C:
				}
			}
			cancel()
			for range a.runtimes {
				<-done
			}
			a.selfRelease.Refresh(context.Background())
			a.tsRelease.Refresh(context.Background())
			for i, name := range names {
				hec := `{"event":{"nodeId":"synthetic","start":"2026-07-26T10:00:00Z","end":"2026-07-26T10:00:01Z","virtualTraffic":[]}}`
				req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/hec/%d", i), strings.NewReader(hec))
				req.SetBasicAuth("", "synthetic-token")
				req.Header.Set("traceparent", fmt.Sprintf("00-%032x-%016x-01", i+1, i+1))
				w := httptest.NewRecorder()
				a.streamSrv.Handler().ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("stream %s: %d %s", name, w.Code, w.Body.String())
				}
				body := fmt.Sprintf(`[{"timestamp":"2026-07-26T10:00:00Z","version":1,"type":"nodeCreated","tailnet":%q,"message":"synthetic","data":{"nodeID":"synthetic"}}]`, name)
				req = httptest.NewRequest(http.MethodPost, "/tailscale/webhook", strings.NewReader(body))
				req.Header.Set("traceparent", fmt.Sprintf("00-%032x-%016x-01", i+1, i+1))
				req.Header.Set("Tailscale-Webhook-Signature", webhookSignature("synthetic-secret", body, time.Unix(1785060000, 0)))
				w = httptest.NewRecorder()
				a.webhookSrv.Handler().ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("webhook %s: %d %s", name, w.Code, w.Body.String())
				}
			}
			flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer flushCancel()
			err = a.Close(flushCtx)
			closed = true
			if err != nil {
				t.Fatalf("Close: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			observed := map[string]map[string]int{}
			ownersByID := map[string]string{}
			for _, span := range spans {
				for _, attr := range span.Attributes {
					if attr.Key == semconv.AttrTailnet {
						ownersByID[hex.EncodeToString(span.SpanId)] = attr.Value.GetStringValue()
					}
				}
			}
			releases := 0
			for _, span := range spans {
				attrs := map[string]string{}
				for _, attr := range span.Attributes {
					if _, ok := attrs[attr.Key]; ok {
						t.Errorf("duplicate %s attribute on %s", attr.Key, span.Name)
					}
					attrs[attr.Key] = attr.Value.GetStringValue()
				}
				owner := attrs[semconv.AttrTailnet]
				if parentOwner, ok := ownersByID[hex.EncodeToString(span.ParentSpanId)]; ok && parentOwner != owner {
					t.Errorf("span %s owner=%s differs from parent=%s", span.Name, owner, parentOwner)
				}
				if strings.HasPrefix(span.Name, "release.check ") {
					releases++
					if owner != "" {
						t.Errorf("process release %s owns tailnet %q", span.Name, owner)
					}
					continue
				}
				if !reveal {
					if owner != "" {
						t.Errorf("PII-disabled span %s leaks owner %q", span.Name, owner)
					}
				} else if owner != names[0] && owner != names[1] {
					t.Errorf("span %s ownership=%q", span.Name, owner)
					continue
				}
				if observed[owner] == nil {
					observed[owner] = map[string]int{}
				}
				observed[owner][span.Name]++
				if reveal && (span.Name == "stream.receive" || span.Name == "webhook.receive") {
					expected := names[0]
					if hex.EncodeToString(span.TraceId) == fmt.Sprintf("%032x", 2) {
						expected = names[1]
					} else if hex.EncodeToString(span.TraceId) != fmt.Sprintf("%032x", 1) {
						t.Errorf("receiver lost supplied trace ID: %x", span.TraceId)
					}
					if owner != expected {
						t.Errorf("receiver %s routed for %s exported as %s", span.Name, expected, owner)
					}
				}
				if reveal && span.Name == "nodemetrics.scrape" {
					expected := "100.64.0.1"
					if owner == names[1] {
						expected = "100.64.0.2"
					}
					if !strings.Contains(attrs["url.full"], expected) {
						t.Errorf("node %s emitted as %s", attrs["url.full"], owner)
					}
				}
				if reveal && strings.HasPrefix(span.Name, "tailscale.api ") && !strings.Contains(attrs["url.full"], owner) {
					t.Errorf("API %s emitted as %s", attrs["url.full"], owner)
				}
			}
			if releases != 2 {
				t.Errorf("release spans=%d, want both process checks", releases)
			}
			if reveal {
				for _, name := range names {
					for _, class := range []string{"scrape nodemetrics", "tailscale.api devices", "nodemetrics.scrape", "stream.receive", "webhook.receive"} {
						if observed[name][class] == 0 {
							t.Errorf("missing %s span for %s (observed %v)", class, name, observed)
						}
					}
				}
			} else {
				for _, class := range []string{"scrape nodemetrics", "tailscale.api devices", "nodemetrics.scrape", "stream.receive", "webhook.receive"} {
					if observed[""][class] < 2 {
						t.Errorf("PII-disabled fixture missing runtime coverage: %s=%d", class, observed[""][class])
					}
				}
			}
		})
	}
}

// Headscale intentionally shares the process provider. A synthetic "headscale"
// tailnet attribute would fabricate ownership; tracing-off must remain a no-op.
func TestRuntimeTraceHeadscaleUnscoped(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("tracing_%t", enabled), func(t *testing.T) {
			var mu sync.Mutex
			var spans []*tracepb.Span
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/user" {
					_, _ = io.WriteString(w, `{"users":[]}`)
					return
				}
				if r.URL.Path == "/v1/traces" {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read traces: %v", err)
						w.WriteHeader(400)
						return
					}
					var req collectortrace.ExportTraceServiceRequest
					if err := proto.Unmarshal(body, &req); err != nil {
						t.Errorf("decode traces: %v", err)
						w.WriteHeader(400)
						return
					}
					mu.Lock()
					defer mu.Unlock()
					for _, rs := range req.ResourceSpans {
						for _, ss := range rs.ScopeSpans {
							spans = append(spans, ss.Spans...)
						}
					}
				}
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer sink.Close()
			cfg := config.Default()
			cfg.Provider = "headscale"
			cfg.Headscale.URL = sink.URL
			cfg.Headscale.APIKey = "synthetic-key"
			cfg.OTLP.Protocol = "http"
			cfg.OTLP.Endpoint = sink.URL
			cfg.OTLP.TLS.Insecure = true
			cfg.OTLP.Compression = "none"
			cfg.OTLP.MetricInterval = config.Duration(time.Hour)
			cfg.Checkpoint.Store = "memory"
			cfg.Checkpoint.EvidenceStore = "memory"
			cfg.Tracing.Enabled = enabled
			cfg.Tracing.Sampler = "always_on"
			a, err := New(context.Background(), cfg, "v-test", discardLog())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := a.runtimes[0].cp.Client.Users(ctx); err != nil {
				t.Errorf("Users: %v", err)
			}
			if err := a.Close(ctx); err != nil {
				t.Fatalf("Close: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !enabled {
				if len(spans) != 0 {
					t.Errorf("tracing disabled exported spans: %v", spans)
				}
				return
			}
			found := false
			for _, span := range spans {
				if span.Name == "headscale.api user" {
					found = true
				}
				for _, attr := range span.Attributes {
					if attr.Key == semconv.AttrTailnet {
						t.Errorf("Headscale fabricated tailnet attribute: %v", attr)
					}
				}
			}
			if !found {
				t.Error("Headscale API span not exported")
			}
		})
	}
}
