package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The small files under examples/config are hand-maintained operator starters,
// not generated artifacts. config.example.yaml remains the exhaustive,
// authoritative configuration reference; these files deliberately contain only
// the first-run choices for one delivery path each.
func TestStarterConfigsLoadAndValidate(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		setup  func(*testing.T)
		assert func(*testing.T, *Config)
	}{
		{
			name: "grafana cloud otlp",
			file: "grafana-cloud-otlp.yaml",
			setup: func(t *testing.T) {
				setStarterTailscaleOAuth(t)
				t.Setenv("TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID", "placeholder-stack-id")
				t.Setenv("TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN", "placeholder-grafana-token")
			},
			assert: func(t *testing.T, c *Config) {
				if c.OTLP.Protocol != "http" {
					t.Errorf("otlp.protocol = %q, want http", c.OTLP.Protocol)
				}
				if c.OTLP.Endpoint != "https://otlp-gateway-REGION.grafana.net/otlp" {
					t.Errorf("otlp.endpoint = %q, want starter endpoint", c.OTLP.Endpoint)
				}
				if c.OTLP.GrafanaCloud.InstanceID != "placeholder-stack-id" {
					t.Errorf("otlp.grafana_cloud.instance_id = %q, want placeholder value", c.OTLP.GrafanaCloud.InstanceID)
				}
				if c.OTLP.GrafanaCloud.Token.Reveal() != "placeholder-grafana-token" {
					t.Errorf("otlp.grafana_cloud.token did not load the placeholder value")
				}
			},
		},
		{
			name:  "prometheus only",
			file:  "prometheus-only.yaml",
			setup: setStarterTailscaleOAuth,
			assert: func(t *testing.T, c *Config) {
				if c.Delivery.Mode != "prometheus" {
					t.Errorf("delivery.mode = %q, want prometheus", c.Delivery.Mode)
				}
				if !c.PrometheusPullEnabled() {
					t.Error("Prometheus pull is disabled for prometheus-only starter")
				}
				if c.Prometheus.Listen != "127.0.0.1:2112" {
					t.Errorf("prometheus.listen = %q, want loopback starter bind", c.Prometheus.Listen)
				}
				if c.Prometheus.Auth.AllowUnauthenticated {
					t.Error("prometheus-only starter should not acknowledge unauthenticated network exposure")
				}
			},
		},
		{
			name:  "stdout",
			file:  "stdout.yaml",
			setup: setStarterTailscaleOAuth,
			assert: func(t *testing.T, c *Config) {
				if c.OTLP.Protocol != "stdout" {
					t.Errorf("otlp.protocol = %q, want stdout", c.OTLP.Protocol)
				}
				if c.OTLP.Endpoint != "" {
					t.Errorf("otlp.endpoint = %q, want empty stdout endpoint", c.OTLP.Endpoint)
				}
				if got := c.OTLP.Stdout.MetricInterval.D(); got != 5*time.Second {
					t.Errorf("otlp.stdout.metric_interval = %s, want 5s", got)
				}
			},
		},
		{
			name: "headscale",
			file: "headscale.yaml",
			setup: func(t *testing.T) {
				t.Setenv("TS2OTEL_HEADSCALE__API_KEY", "placeholder-headscale-api-key")
			},
			assert: func(t *testing.T, c *Config) {
				if c.Provider != "headscale" {
					t.Errorf("provider = %q, want headscale", c.Provider)
				}
				if c.Headscale.URL != "https://headscale.example.org" {
					t.Errorf("headscale.url = %q, want starter endpoint", c.Headscale.URL)
				}
				if c.Headscale.APIKey.Reveal() != "placeholder-headscale-api-key" {
					t.Errorf("headscale.api_key did not load the placeholder value")
				}
				if c.Tailscale.Auth.OAuth.ClientID != "" || c.Tailscale.Auth.OAuth.ClientSecret.Reveal() != "" {
					t.Error("headscale starter should not configure Tailscale OAuth credentials")
				}
			},
		},
		{
			name:  "multi-tailnet",
			file:  "multi-tailnet.yaml",
			setup: setStarterTailscaleOAuth,
			assert: func(t *testing.T, c *Config) {
				if len(c.Tailnets) != 2 {
					t.Fatalf("tailnets = %d entries, want 2", len(c.Tailnets))
				}
				for i, want := range []string{"acme.example.com", "beta.example.com"} {
					if c.Tailnets[i].Name != want {
						t.Errorf("tailnets[%d].name = %q, want %q", i, c.Tailnets[i].Name, want)
					}
					if c.Tailnets[i].Auth.Method != "oauth" {
						t.Errorf("tailnets[%d].auth.method = %q, want oauth", i, c.Tailnets[i].Auth.Method)
					}
					if c.Tailnets[i].Auth.OAuth.ClientID != "" || c.Tailnets[i].Auth.OAuth.ClientSecret.Reveal() != "" {
						t.Errorf("tailnets[%d] should leave OAuth credentials empty for the name-keyed overlay", i)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			path := filepath.Join("..", "..", "examples", "config", tt.file)
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read starter %s: %v", path, err)
			}
			for _, marker := range []string{"hand-maintained", "config.example.yaml", "_file", "-preflight"} {
				if !strings.Contains(string(contents), marker) {
					t.Errorf("starter %s does not document %q", tt.file, marker)
				}
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load(%s) = %v", path, err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate(%s) = %v", path, err)
			}
			if tt.name != "headscale" {
				if cfg.Tailscale.Auth.OAuth.ClientID != "placeholder-client-id" {
					t.Errorf("tailscale.auth.oauth.client_id = %q, want placeholder value", cfg.Tailscale.Auth.OAuth.ClientID)
				}
				if cfg.Tailscale.Auth.OAuth.ClientSecret.Reveal() != "placeholder-client-secret" {
					t.Errorf("tailscale.auth.oauth.client_secret did not load the placeholder value")
				}
			}
			tt.assert(t, cfg)
		})
	}
}

func setStarterTailscaleOAuth(t *testing.T) {
	t.Helper()
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID", "placeholder-client-id")
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET", "placeholder-client-secret")
}
