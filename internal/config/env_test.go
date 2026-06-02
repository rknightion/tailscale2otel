package config_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/internal/config"
)

const envYAML = `
tailscale:
  tailnet: "${TS_TAILNET}"
  auth:
    method: apikey
    apikey: "$TS_APIKEY"
otlp:
  grafana_cloud:
    instance_id: "${GC_INSTANCE}"
    token: "${GC_TOKEN}"
  headers:
    Authorization: "Bearer ${GC_TOKEN}"
`

func TestLoadExpandsEnv(t *testing.T) {
	t.Setenv("TS_TAILNET", "env-tailnet.org")
	t.Setenv("TS_APIKEY", "tskey-from-env")
	t.Setenv("GC_INSTANCE", "999")
	t.Setenv("GC_TOKEN", "secret-token")

	p := writeTemp(t, envYAML)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Tailscale.Tailnet != "env-tailnet.org" {
		t.Errorf("Tailnet = %q, want env-tailnet.org", cfg.Tailscale.Tailnet)
	}
	if cfg.Tailscale.Auth.APIKey != "tskey-from-env" {
		t.Errorf("APIKey = %q, want tskey-from-env (from $VAR form)", cfg.Tailscale.Auth.APIKey)
	}
	if cfg.OTLP.GrafanaCloud.InstanceID != "999" {
		t.Errorf("InstanceID = %q, want 999", cfg.OTLP.GrafanaCloud.InstanceID)
	}
	if cfg.OTLP.Headers["Authorization"] != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want Bearer secret-token", cfg.OTLP.Headers["Authorization"])
	}
}

func TestLoadExpandsUnknownEnvToEmpty(t *testing.T) {
	// Ensure the variable is genuinely unset.
	const yaml = `
tailscale:
  auth:
    method: apikey
    apikey: "${DEFINITELY_UNSET_VAR_XYZ}"
`
	p := writeTemp(t, yaml)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tailscale.Auth.APIKey != "" {
		t.Errorf("APIKey = %q, want empty string for unknown env var", cfg.Tailscale.Auth.APIKey)
	}
}
