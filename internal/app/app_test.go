package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/config"
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
