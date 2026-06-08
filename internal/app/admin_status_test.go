package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

func TestStatusPage_HTMLRenders(t *testing.T) {
	cfg := config.Default() // landing_page defaults to true
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		"<!DOCTYPE html>", serviceName, "vtest", "Collectors", "Metrics catalog",
		`id="healthBadge"`, // at-a-glance health verdict in the header
		`id="collBody"`,    // collectors table body that the poller live-rebuilds
		`id="apiBody"`,     // API-health section table body
		"API health",       // new API section heading
		`id="staleBanner"`, // freshness indicator shown on poll failure
		"drawSpark",        // inline-SVG sparkline renderer
	} {
		if !strings.Contains(body, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func TestStatusPage_UnknownPath404(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /nope = %d, want 404 (only / renders the page)", w.Code)
	}
}

func TestStatusJSON_Shape(t *testing.T) {
	cfg := config.Default()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/status.json = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got statusdata.Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status json: %v", err)
	}
	if got.Service.Name != serviceName || got.Service.Version != "vtest" {
		t.Errorf("service = %+v, want name=%s version=vtest", got.Service, serviceName)
	}
	if len(got.Collectors) != len(a.runtimes[0].registry.Entries()) {
		t.Errorf("collectors = %d, want %d (one per registered collector)", len(got.Collectors), len(a.runtimes[0].registry.Entries()))
	}
	if len(got.Metrics) == 0 {
		t.Errorf("metrics catalog is empty")
	}
	// Self-obs is off in the default config, so live cardinality is unavailable.
	if got.Cardinality.Available {
		t.Errorf("cardinality should be unavailable when self-observability is off")
	}
	// buildStatus must always set a valid health verdict (deriveHealth's logic is
	// covered exhaustively in health_test.go).
	switch got.Health {
	case healthHealthy, healthDegraded, healthStarting:
	default:
		t.Errorf("health = %q, want one of healthy/degraded/starting", got.Health)
	}
}

func TestStatusPage_RedactsSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Auth.APIKey = "tskey-SECRETAPIKEY"
	cfg.Tailscale.Auth.OAuth.ClientSecret = "SECRETOAUTH"
	cfg.OTLP.GrafanaCloud.Token = "SECRETGCTOKEN"
	cfg.Streaming.Token = "SECRETSTREAM"
	cfg.Webhook.Secret = "SECRETWEBHOOK"
	cfg.Profiling.Pyroscope.BasicAuthPassword = "SECRETPYRO"
	cfg.OTLP.Headers = map[string]string{"Authorization": "Basic SECRETHEADER", "X-Scope-OrgID": "tenant-1"}

	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	secrets := []string{"SECRETAPIKEY", "SECRETOAUTH", "SECRETGCTOKEN", "SECRETSTREAM", "SECRETWEBHOOK", "SECRETPYRO", "SECRETHEADER", "tskey-SECRETAPIKEY", "Basic SECRETHEADER"}
	for _, path := range []string{"/", "/api/status.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		body := w.Body.String()
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaked secret %q", path, secret)
			}
		}
	}

	// The JSON must still report that the secrets ARE configured (booleans), and
	// header KEYS (not values) may appear.
	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	var got statusdata.Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cs := got.Config
	if !cs.APIKeySet || !cs.OAuthSecretSet || !cs.GCloudTokenSet || !cs.StreamTokenSet || !cs.WebhookSecretSet || !cs.PyroscopeAuthSet {
		t.Errorf("expected all *Set booleans true, got %+v", cs)
	}
	if !strings.Contains(strings.Join(cs.OTLPHeaderKeys, ","), "Authorization") {
		t.Errorf("OTLP header KEYS should include Authorization, got %v", cs.OTLPHeaderKeys)
	}
}

func TestAdminServer_ProbesStillOK(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK || w.Body.String() != "ok" {
			t.Fatalf("GET %s = %d %q, want 200 ok", path, w.Code, w.Body.String())
		}
	}
}

func TestAdminServer_PprofGatedByConfig(t *testing.T) {
	t.Run("disabled -> 404", func(t *testing.T) {
		a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
		srv := a.buildAdminServer()
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("pprof disabled: GET /debug/pprof/ = %d, want 404", w.Code)
		}
	})
	t.Run("enabled -> 200", func(t *testing.T) {
		cfg := config.Default()
		cfg.Profiling.Pprof.Enabled = true
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		srv := a.buildAdminServer()
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("pprof enabled: GET /debug/pprof/ = %d, want 200", w.Code)
		}
	})
}
