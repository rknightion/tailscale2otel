package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/config"
)

func metricsTestApp(token string) *App {
	cfg := &config.Config{}
	cfg.Prometheus.Auth.Token = config.Secret(token)
	return &App{cfg: cfg}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestRequireMetricsAuth(t *testing.T) {
	t.Run("open when no token", func(t *testing.T) {
		h := metricsTestApp("").requireMetricsAuth(okHandler())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rr.Code != http.StatusOK {
			t.Errorf("open path code = %d, want 200", rr.Code)
		}
	})
	t.Run("401 without credentials", func(t *testing.T) {
		h := metricsTestApp("sekret").requireMetricsAuth(okHandler())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("no-cred code = %d, want 401", rr.Code)
		}
	})
	t.Run("200 with bearer", func(t *testing.T) {
		h := metricsTestApp("sekret").requireMetricsAuth(okHandler())
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer sekret")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("bearer code = %d, want 200", rr.Code)
		}
	})
	t.Run("200 with basic", func(t *testing.T) {
		h := metricsTestApp("sekret").requireMetricsAuth(okHandler())
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.SetBasicAuth("scrape", "sekret")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("basic code = %d, want 200", rr.Code)
		}
	})
}
