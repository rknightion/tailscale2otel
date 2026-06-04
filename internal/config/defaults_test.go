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
	if !cfg.Cardinality.DevicePerEntity || !cfg.Cardinality.UserPerEntity || !cfg.Cardinality.KeyPerEntity {
		t.Errorf("Cardinality per-entity toggles = %v/%v/%v, want all default true",
			cfg.Cardinality.DevicePerEntity, cfg.Cardinality.UserPerEntity, cfg.Cardinality.KeyPerEntity)
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
	if cfg.SelfObservability.InstanceID != "" {
		t.Errorf("SelfObservability.InstanceID = %q, want default empty", cfg.SelfObservability.InstanceID)
	}
	if !cfg.Admin.LandingPage {
		t.Errorf("Admin.LandingPage = false, want default true")
	}
}

// TestProfilingDefaults pins the off-by-default continuous-profiling block:
// pprof + pyroscope disabled, the pyroscope upload_rate defaulting to 15s, and
// the mutex/block fractions left at 0 (sampling disabled).
func TestProfilingDefaults(t *testing.T) {
	cfg := config.Default()
	p := cfg.Profiling
	if p.Pprof.Enabled {
		t.Errorf("Profiling.Pprof.Enabled = true, want default false")
	}
	if p.Pyroscope.Enabled {
		t.Errorf("Profiling.Pyroscope.Enabled = true, want default false")
	}
	if p.Pyroscope.UploadRate.D() != 15*time.Second {
		t.Errorf("Profiling.Pyroscope.UploadRate = %v, want default 15s", p.Pyroscope.UploadRate.D())
	}
	if p.MutexProfileFraction != 0 {
		t.Errorf("Profiling.MutexProfileFraction = %d, want default 0", p.MutexProfileFraction)
	}
	if p.BlockProfileRate != 0 {
		t.Errorf("Profiling.BlockProfileRate = %d, want default 0", p.BlockProfileRate)
	}
}

// TestLoadAdminLandingPageFalseOverridesDefaultTrue mirrors the per-collector
// "present false overrides default true" caveat for admin.landing_page.
func TestLoadAdminLandingPageFalseOverridesDefaultTrue(t *testing.T) {
	const y = `
admin:
  enabled: true
  landing_page: false
`
	p := writeTemp(t, y)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Admin.LandingPage {
		t.Errorf("Admin.LandingPage = true, want false (explicit override of default true)")
	}
	// Sibling default untouched.
	if cfg.Admin.Listen != ":9090" {
		t.Errorf("Admin.Listen = %q, want default :9090 preserved", cfg.Admin.Listen)
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
  device_per_entity: false
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
	if cfg.Cardinality.DevicePerEntity {
		t.Errorf("Cardinality.DevicePerEntity = true, want false (explicit override)")
	}
	// Sibling defaults untouched.
	if cfg.Collectors.Devices.Interval.D() != 60*time.Second {
		t.Errorf("Devices.Interval = %v, want default 60s preserved", cfg.Collectors.Devices.Interval.D())
	}
	if !cfg.Cardinality.CollapseExternal {
		t.Errorf("Cardinality.CollapseExternal = false, want default true preserved")
	}
	if !cfg.Cardinality.UserPerEntity || !cfg.Cardinality.KeyPerEntity {
		t.Errorf("sibling per-entity toggles = %v/%v, want default true preserved",
			cfg.Cardinality.UserPerEntity, cfg.Cardinality.KeyPerEntity)
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

func TestNodeMetricsDiscoveryDefaults(t *testing.T) {
	d := config.Default().Collectors.NodeMetrics.Discovery
	if d.Enabled {
		t.Errorf("Discovery.Enabled = true, want default false")
	}
	if d.Interval.D() != 5*time.Minute {
		t.Errorf("Discovery.Interval = %v, want default 5m", d.Interval.D())
	}
	if d.Scheme != "http" {
		t.Errorf("Discovery.Scheme = %q, want http", d.Scheme)
	}
	if d.Port != 5252 {
		t.Errorf("Discovery.Port = %d, want 5252", d.Port)
	}
	if d.Path != "/metrics" {
		t.Errorf("Discovery.Path = %q, want /metrics", d.Path)
	}
	if !d.OnlineOnly {
		t.Errorf("Discovery.OnlineOnly = false, want default true")
	}
	if !d.ExcludeExternal {
		t.Errorf("Discovery.ExcludeExternal = false, want default true")
	}
	if d.AddressOrder != "ipv4" {
		t.Errorf("Discovery.AddressOrder = %q, want ipv4", d.AddressOrder)
	}
	if d.InstanceSource != "address" {
		t.Errorf("Discovery.InstanceSource = %q, want address", d.InstanceSource)
	}
	if !d.IncludeHostLabels {
		t.Errorf("Discovery.IncludeHostLabels = false, want default true")
	}
	if !d.IncludeTagsLabel {
		t.Errorf("Discovery.IncludeTagsLabel = false, want default true")
	}
}

func TestNodeMetricsDiscoveryParse(t *testing.T) {
	const y = `
collectors:
  node_metrics:
    enabled: true
    discovery:
      enabled: true
      interval: 2m
      port: 9100
      online_only: false
      include_tags: ["tag:server"]
`
	p := writeTemp(t, y)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := cfg.Collectors.NodeMetrics.Discovery
	if !d.Enabled || d.Interval.D() != 2*time.Minute || d.Port != 9100 {
		t.Fatalf("discovery enabled/interval/port = %v/%v/%d", d.Enabled, d.Interval.D(), d.Port)
	}
	// An explicit false overrides the true default.
	if d.OnlineOnly {
		t.Errorf("Discovery.OnlineOnly = true, want false (explicit override)")
	}
	// An unset bool keeps its true default.
	if !d.ExcludeExternal {
		t.Errorf("Discovery.ExcludeExternal = false, want true (default preserved)")
	}
	if len(d.IncludeTags) != 1 || d.IncludeTags[0] != "tag:server" {
		t.Errorf("Discovery.IncludeTags = %v, want [tag:server]", d.IncludeTags)
	}
	// An unset enum keeps its default.
	if d.Scheme != "http" {
		t.Errorf("Discovery.Scheme = %q, want http (default preserved)", d.Scheme)
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
