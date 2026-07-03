package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetry/pii"
)

// TestMetricsHandler_DuplicateSeriesReturns200 pins #103: with
// pii_filter.tailnet_name=false the per-provider registries produce byte-identical
// series (per-tailnet in multi mode; process+tailnet self-obs in single mode). The
// default promhttp error handling would turn that Gather collision into a permanent
// HTTP 500; ContinueOnError must keep the scrape returning 200 in both modes.
func TestMetricsHandler_DuplicateSeriesReturns200(t *testing.T) {
	ctx := context.Background()
	piiOff := pii.Categories{pii.CatTailnetName: false}
	cases := []struct {
		name     string
		tailnets []telemetry.PerTailnetOptions
	}{
		{"multi-tailnet", []telemetry.PerTailnetOptions{{Name: "alpha", InstanceID: "i/alpha"}, {Name: "beta", InstanceID: "i/beta"}}},
		{"single-tailnet", []telemetry.PerTailnetOptions{{Name: "solo", InstanceID: "i"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps, err := telemetry.NewProviderSet(ctx, telemetry.Options{
				ServiceName: "tailscale2otel", Provider: "tailscale",
				PrometheusEnabled: true, Protocol: "stdout", StdoutWriter: io.Discard,
				PIIFilter: piiOff,
			}, tc.tailnets)
			if err != nil {
				t.Fatalf("NewProviderSet: %v", err)
			}
			defer func() { _ = ps.Shutdown(ctx) }()

			// Every provider emits the same self-obs counter; each tailnet also emits
			// the same inventory gauge. With tailscale.tailnet dropped these collide.
			ps.Process().Emitter().Counter("tailscale2otel.export.datapoints", "1", "d", 1, nil)
			for _, tn := range tc.tailnets {
				e := ps.Tailnet(tn.Name).Emitter()
				e.Counter("tailscale2otel.export.datapoints", "1", "d", 1, nil)
				e.Gauge("tailscale.devices.count", "1", "d", 1, nil)
			}

			a := &App{cfg: &config.Config{}}
			srv := a.buildMetricsServer(ps.PromGatherers())
			rr := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: /metrics code = %d, want 200; body:\n%s", tc.name, rr.Code, rr.Body.String())
			}
		})
	}
}

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
