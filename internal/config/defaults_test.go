package config_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
)

func TestLoadAppliesDefaultsWhenOmitted(t *testing.T) {
	// A minimal document: only set one unrelated field so the file is valid
	// but virtually everything falls back to defaults.
	p := writeTemp(t, "log_level: warn\n")
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn (the one set field)", cfg.LogLevel)
	}
	if cfg.Tailscale.Tailnet != "example.com" {
		t.Errorf("Tailnet = %q, want default example.com", cfg.Tailscale.Tailnet)
	}
	if cfg.Tailscale.Auth.Method != "oauth" {
		t.Errorf("Auth.Method = %q, want default oauth", cfg.Tailscale.Auth.Method)
	}
	if got := cfg.Tailscale.Auth.OAuth.TokenURL; got != "https://api.tailscale.com/api/v2/oauth/token" {
		t.Errorf("OAuth.TokenURL = %q, want default token url", got)
	}
	if got := cfg.Tailscale.Auth.OAuth.Scopes; len(got) != 1 || got[0] != "all:read" {
		t.Errorf("OAuth.Scopes = %v, want default [all:read]", got)
	}
	if cfg.Tailscale.HTTP.Timeout.D() != 30*time.Second {
		t.Errorf("HTTP.Timeout = %v, want default 30s", cfg.Tailscale.HTTP.Timeout.D())
	}
	if cfg.Tailscale.HTTP.Retry.MaxAttempts != 4 {
		t.Errorf("Retry.MaxAttempts = %d, want default 4", cfg.Tailscale.HTTP.Retry.MaxAttempts)
	}
	if cfg.Tailscale.HTTP.Retry.MaxDelay.D() != 10*time.Second {
		t.Errorf("Retry.MaxDelay = %v, want default 10s", cfg.Tailscale.HTTP.Retry.MaxDelay.D())
	}
	if cfg.OTLP.Protocol != "http" {
		t.Errorf("OTLP.Protocol = %q, want default http", cfg.OTLP.Protocol)
	}
	if cfg.OTLP.Endpoint != "https://otlp-gateway-prod-us-central-0.grafana.net/otlp" {
		t.Errorf("OTLP.Endpoint = %q, want default grafana endpoint", cfg.OTLP.Endpoint)
	}
	if cfg.OTLP.MetricInterval.D() != 30*time.Second {
		t.Errorf("MetricInterval = %v, want default 30s", cfg.OTLP.MetricInterval.D())
	}
	if cfg.Enrichment.CacheTTL.D() != 5*time.Minute {
		t.Errorf("Enrichment.CacheTTL = %v, want default 5m", cfg.Enrichment.CacheTTL.D())
	}
	// Default true booleans.
	if !cfg.Cardinality.FlowNodeDims {
		t.Errorf("Cardinality.FlowNodeDims = false, want default true")
	}
	if !cfg.Cardinality.CollapseExternal {
		t.Errorf("Cardinality.CollapseExternal = false, want default true")
	}
	if !cfg.Collectors.Devices.Enabled {
		t.Errorf("Devices.Enabled = false, want default true")
	}
	if cfg.Collectors.Flowlogs.Source != "poll" {
		t.Errorf("Flowlogs.Source = %q, want default poll", cfg.Collectors.Flowlogs.Source)
	}
	if cfg.Collectors.Flowlogs.LogMode != "per_connection" {
		t.Errorf("Flowlogs.LogMode = %q, want default per_connection", cfg.Collectors.Flowlogs.LogMode)
	}
	if cfg.Collectors.Flowlogs.MaxWindow.D() != time.Hour {
		t.Errorf("Flowlogs.MaxWindow = %v, want default 1h", cfg.Collectors.Flowlogs.MaxWindow.D())
	}
	if cfg.Collectors.Auditlogs.MaxWindow.D() != 6*time.Hour {
		t.Errorf("Auditlogs.MaxWindow = %v, want default 6h", cfg.Collectors.Auditlogs.MaxWindow.D())
	}
	if cfg.Collectors.Keys.ExpiryWarn.D() != 168*time.Hour {
		t.Errorf("Keys.ExpiryWarn = %v, want default 168h", cfg.Collectors.Keys.ExpiryWarn.D())
	}
	if cfg.Collectors.Settings.Interval.D() != 600*time.Second {
		t.Errorf("Settings.Interval = %v, want default 600s", cfg.Collectors.Settings.Interval.D())
	}
	if cfg.Checkpoint.Store != "memory" {
		t.Errorf("Checkpoint.Store = %q, want default memory", cfg.Checkpoint.Store)
	}
	if cfg.Checkpoint.FilePath != "/var/lib/tailscale2otel/checkpoints.json" {
		t.Errorf("Checkpoint.FilePath = %q, want default path", cfg.Checkpoint.FilePath)
	}
	if cfg.Streaming.Listen != ":8088" || cfg.Streaming.Path != "/services/collector/event" {
		t.Errorf("Streaming defaults = %q %q, want :8088 /services/collector/event", cfg.Streaming.Listen, cfg.Streaming.Path)
	}
	if cfg.Streaming.Decompress != "auto" {
		t.Errorf("Streaming.Decompress = %q, want default auto", cfg.Streaming.Decompress)
	}
	if cfg.Webhook.Listen != ":8089" || cfg.Webhook.Path != "/tailscale/webhook" {
		t.Errorf("Webhook defaults = %q %q, want :8089 /tailscale/webhook", cfg.Webhook.Listen, cfg.Webhook.Path)
	}
	if !cfg.SelfObservability.Enabled {
		t.Errorf("SelfObservability.Enabled = false, want default true")
	}
}

// TestLoadPresentFalseOverridesDefaultTrue documents the key caveat: a key
// explicitly present in YAML wins even when its value is the zero value, so
// "enabled: false" overrides the default true.
func TestLoadPresentFalseOverridesDefaultTrue(t *testing.T) {
	const y = `
collectors:
  devices:
    enabled: false
self_observability:
  enabled: false
cardinality:
  flow_node_dims: false
`
	p := writeTemp(t, y)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Collectors.Devices.Enabled {
		t.Errorf("Devices.Enabled = true, want false (explicit override of default true)")
	}
	if cfg.SelfObservability.Enabled {
		t.Errorf("SelfObservability.Enabled = true, want false (explicit override)")
	}
	if cfg.Cardinality.FlowNodeDims {
		t.Errorf("Cardinality.FlowNodeDims = true, want false (explicit override)")
	}
	// Sibling defaults untouched.
	if cfg.Collectors.Devices.Interval.D() != 60*time.Second {
		t.Errorf("Devices.Interval = %v, want default 60s preserved", cfg.Collectors.Devices.Interval.D())
	}
	if !cfg.Cardinality.CollapseExternal {
		t.Errorf("Cardinality.CollapseExternal = false, want default true preserved")
	}
}

func TestDefaultIsValid(t *testing.T) {
	if err := config.Default().Validate(); err != nil {
		t.Fatalf("Default() should be valid: %v", err)
	}
}
