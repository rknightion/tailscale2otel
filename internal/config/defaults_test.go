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
	if cfg.OTLP.MetricInterval.D() != 60*time.Second {
		t.Errorf("MetricInterval = %v, want default 60s", cfg.OTLP.MetricInterval.D())
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

func TestNodeMetricsDefaults(t *testing.T) {
	cfg := config.Default()
	nm := cfg.Collectors.NodeMetrics
	if nm.Enabled {
		t.Errorf("NodeMetrics.Enabled = true, want default false")
	}
	if nm.Interval.D() != 60*time.Second {
		t.Errorf("NodeMetrics.Interval = %v, want default 60s", nm.Interval.D())
	}
	if nm.Timeout.D() != 10*time.Second {
		t.Errorf("NodeMetrics.Timeout = %v, want default 10s", nm.Timeout.D())
	}
	if len(nm.Targets) != 0 {
		t.Errorf("NodeMetrics.Targets = %v, want empty by default", nm.Targets)
	}
}

func TestNodeMetricsTargetsParse(t *testing.T) {
	const y = `
collectors:
  node_metrics:
    enabled: true
    interval: 30s
    targets:
      - url: http://100.64.0.1:5252/metrics
        instance: nodeA
        labels:
          role: relay
      - url: http://100.64.0.2:5252/metrics
`
	p := writeTemp(t, y)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	nm := cfg.Collectors.NodeMetrics
	if !nm.Enabled || nm.Interval.D() != 30*time.Second {
		t.Fatalf("node_metrics enabled/interval = %v/%v", nm.Enabled, nm.Interval.D())
	}
	// Interval set, Timeout omitted -> default preserved.
	if nm.Timeout.D() != 10*time.Second {
		t.Errorf("NodeMetrics.Timeout = %v, want default 10s preserved", nm.Timeout.D())
	}
	if len(nm.Targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(nm.Targets))
	}
	if nm.Targets[0].URL != "http://100.64.0.1:5252/metrics" || nm.Targets[0].Instance != "nodeA" {
		t.Errorf("target0 = %+v", nm.Targets[0])
	}
	if nm.Targets[0].Labels["role"] != "relay" {
		t.Errorf("target0 labels = %v", nm.Targets[0].Labels)
	}
	if nm.Targets[1].URL != "http://100.64.0.2:5252/metrics" {
		t.Errorf("target1 = %+v", nm.Targets[1])
	}
}
