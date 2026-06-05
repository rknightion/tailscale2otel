package config_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
	"gopkg.in/yaml.v3"
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
	if cfg.Tailscale.Tailnet != "-" {
		t.Errorf("Tailnet = %q, want default \"-\" (auth principal's default tailnet)", cfg.Tailscale.Tailnet)
	}
	if cfg.Tailscale.Auth.Method != "oauth" {
		t.Errorf("Auth.Method = %q, want default oauth", cfg.Tailscale.Auth.Method)
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
	if cfg.Enrichment.ReverseDNS.Enabled {
		t.Errorf("Enrichment.ReverseDNS.Enabled = true, want default false (opt-in)")
	}
	if rd := cfg.Enrichment.ReverseDNS; rd.Timeout.D() != 2*time.Second || rd.CacheTTL.D() != time.Hour ||
		rd.NegativeTTL.D() != 5*time.Minute || rd.MaxEntries != 4096 {
		t.Errorf("Enrichment.ReverseDNS defaults = timeout %v / ttl %v / neg %v / max %d, want 2s/1h/5m/4096",
			rd.Timeout.D(), rd.CacheTTL.D(), rd.NegativeTTL.D(), rd.MaxEntries)
	}
	// Default true booleans.
	if !cfg.Cardinality.Flow.NodeDims {
		t.Errorf("Cardinality.Flow.NodeDims = false, want default true")
	}
	if !cfg.Cardinality.Flow.CollapseExternal {
		t.Errorf("Cardinality.Flow.CollapseExternal = false, want default true")
	}
	if !cfg.Cardinality.PerEntity.Device || !cfg.Cardinality.PerEntity.User || !cfg.Cardinality.PerEntity.Key {
		t.Errorf("Cardinality per-entity toggles = %v/%v/%v, want all default true",
			cfg.Cardinality.PerEntity.Device, cfg.Cardinality.PerEntity.User, cfg.Cardinality.PerEntity.Key)
	}
	if cfg.Cardinality.MetricLimit != 10000 {
		t.Errorf("Cardinality.MetricLimit = %d, want default 10000", cfg.Cardinality.MetricLimit)
	}
	// Flow metrics default to the bounded rollup family with a top-N of 500.
	if cfg.Cardinality.Flow.MetricsMode != "rollup" {
		t.Errorf("Cardinality.Flow.MetricsMode = %q, want default rollup", cfg.Cardinality.Flow.MetricsMode)
	}
	if cfg.Cardinality.Flow.RollupTopN != 500 {
		t.Errorf("Cardinality.Flow.RollupTopN = %d, want default 500", cfg.Cardinality.Flow.RollupTopN)
	}
	// Flow metric port/service toggles default off (ports stay off metrics by default).
	if cfg.Cardinality.Flow.SourcePort || cfg.Cardinality.Flow.DestinationPort || cfg.Cardinality.Flow.DestinationService {
		t.Errorf("Cardinality flow toggles = src %v / dst %v / service %v, want all default false",
			cfg.Cardinality.Flow.SourcePort, cfg.Cardinality.Flow.DestinationPort, cfg.Cardinality.Flow.DestinationService)
	}
	if !cfg.Collectors.Devices.Enabled {
		t.Errorf("Devices.Enabled = false, want default true")
	}
	if cfg.Collectors.Devices.PostureLogMode != "changes" {
		t.Errorf("Devices.PostureLogMode = %q, want default changes", cfg.Collectors.Devices.PostureLogMode)
	}
	// Opt-out populated default: the integration namespaces plus ip are promoted
	// to attribute metrics out of the box once collect_posture is enabled.
	wantNS := []string{"intune", "jamf", "kandji", "crowdstrike", "sentinelone", "kolide", "ip"}
	gotNS := cfg.Collectors.Devices.AttributeNamespaces
	if len(gotNS) != len(wantNS) {
		t.Errorf("Devices.AttributeNamespaces = %v, want %v", gotNS, wantNS)
	} else {
		for i := range wantNS {
			if gotNS[i] != wantNS[i] {
				t.Errorf("Devices.AttributeNamespaces[%d] = %q, want %q", i, gotNS[i], wantNS[i])
				break
			}
		}
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
	if cfg.Checkpoint.Store != "file" {
		t.Errorf("Checkpoint.Store = %q, want default file", cfg.Checkpoint.Store)
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
	if cfg.Webhook.Tolerance.D() != 5*time.Minute {
		t.Errorf("Webhook.Tolerance = %v, want 5m default (replay protection)", cfg.Webhook.Tolerance.D())
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
// pprof + pyroscope disabled, the pyroscope upload_rate defaulting to 60s, and
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
	if p.Pyroscope.UploadRate.D() != 60*time.Second {
		t.Errorf("Profiling.Pyroscope.UploadRate = %v, want default 60s", p.Pyroscope.UploadRate.D())
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
  flow:
    node_dims: false
  per_entity:
    device: false
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
	if cfg.Cardinality.Flow.NodeDims {
		t.Errorf("Cardinality.Flow.NodeDims = true, want false (explicit override)")
	}
	if cfg.Cardinality.PerEntity.Device {
		t.Errorf("Cardinality.PerEntity.Device = true, want false (explicit override)")
	}
	// Sibling defaults untouched.
	if cfg.Collectors.Devices.Interval.D() != 60*time.Second {
		t.Errorf("Devices.Interval = %v, want default 60s preserved", cfg.Collectors.Devices.Interval.D())
	}
	if !cfg.Cardinality.Flow.CollapseExternal {
		t.Errorf("Cardinality.Flow.CollapseExternal = false, want default true preserved")
	}
	if !cfg.Cardinality.PerEntity.User || !cfg.Cardinality.PerEntity.Key {
		t.Errorf("sibling per-entity toggles = %v/%v, want default true preserved",
			cfg.Cardinality.PerEntity.User, cfg.Cardinality.PerEntity.Key)
	}
}

func TestDefaultIsValid(t *testing.T) {
	if err := config.Default().Validate(); err != nil {
		t.Fatalf("Default() should be valid: %v", err)
	}
}

// TestExampleConfigMatchesDefaults guards that config.example.yaml stays aligned
// with the in-code Default(): loading the shipped example must yield exactly the
// same configuration as Default(), so the example faithfully documents our real
// defaults and never silently overrides them. The comparison is on the YAML
// encoding, which normalizes nil-vs-empty collections (an omitted list and an
// explicit [] are the same configuration). If this fails, you changed a default
// in code (defaults.go) or in the example without updating the other.
func TestExampleConfigMatchesDefaults(t *testing.T) {
	ex, err := config.Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	exYAML, err := yaml.Marshal(ex)
	if err != nil {
		t.Fatalf("marshal example: %v", err)
	}
	defYAML, err := yaml.Marshal(config.Default())
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	if string(exYAML) != string(defYAML) {
		t.Errorf("config.example.yaml has drifted from config.Default() — update whichever is stale so the "+
			"example documents the real defaults.\n--- config.example.yaml ---\n%s\n--- config.Default() ---\n%s", exYAML, defYAML)
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
	if nm.MaxResponseBytes != 4*1024*1024 {
		t.Errorf("NodeMetrics.MaxResponseBytes = %d, want 4MiB", nm.MaxResponseBytes)
	}
	if nm.MaxSamples != 50000 {
		t.Errorf("NodeMetrics.MaxSamples = %d, want 50000", nm.MaxSamples)
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
	if d.MaxTargets != 1000 {
		t.Errorf("Discovery.MaxTargets = %d, want 1000", d.MaxTargets)
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
	if d.InstanceSource != "name" {
		t.Errorf("Discovery.InstanceSource = %q, want name", d.InstanceSource)
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
      max_targets: 12
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
	if !d.Enabled || d.Interval.D() != 2*time.Minute || d.Port != 9100 || d.MaxTargets != 12 {
		t.Fatalf("discovery enabled/interval/port/max_targets = %v/%v/%d/%d", d.Enabled, d.Interval.D(), d.Port, d.MaxTargets)
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
    max_response_bytes: 2048
    max_samples: 123
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
	if nm.MaxResponseBytes != 2048 || nm.MaxSamples != 123 {
		t.Fatalf("node_metrics max_response_bytes/max_samples = %d/%d", nm.MaxResponseBytes, nm.MaxSamples)
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
