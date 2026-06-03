package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
)

// TestConfigExampleLoadsAndValidates guards the shipped config.example.yaml
// against drift: it must always parse, env-expand, and validate cleanly.
func TestConfigExampleLoadsAndValidates(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("config.example.yaml not found: %v", err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("config.example.yaml must load and validate: %v", err)
	}
}

// writeTemp writes content to a file in a fresh temp dir and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

const representativeYAML = `
log_level: debug
tailscale:
  tailnet: "acme.org"
  auth:
    method: apikey
    apikey: "tskey-abc"
    oauth:
      client_id: "cid"
      client_secret: "csecret"
      scopes: ["all:read", "devices:read"]
  http:
    timeout: 45s
    retry:
      max_attempts: 7
      base_delay: 250ms
      max_delay: 20s
otlp:
  protocol: grpc
  endpoint: "https://example.test/otlp"
  grafana_cloud:
    instance_id: "12345"
    token: "glc_token"
  headers:
    X-Scope-OrgID: "tenant-1"
  tls:
    insecure: true
    ca_file: "/etc/ca.pem"
  metric_interval: 15s
enrichment:
  cache_ttl: 10m
cardinality:
  flow_include_ports: true
  flow_node_dims: false
  collapse_external: false
collectors:
  devices:
    enabled: false
    interval: 90s
    collect_routes: true
    collect_posture: true
  flowlogs:
    source: stream
    lag: 200s
    log_mode: per_record
  keys:
    expiry_warn: 72h
checkpoint:
  store: file
  file_path: "/tmp/cp.json"
streaming:
  enabled: true
  listen: ":9000"
  token: "stoken"
  decompress: gzip
webhook:
  enabled: true
  secret: "wsecret"
self_observability:
  enabled: false
`

func TestLoadNestedValues(t *testing.T) {
	p := writeTemp(t, representativeYAML)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.Tailscale.Tailnet != "acme.org" {
		t.Errorf("Tailnet = %q, want acme.org", cfg.Tailscale.Tailnet)
	}
	if cfg.Tailscale.Auth.Method != "apikey" {
		t.Errorf("Auth.Method = %q, want apikey", cfg.Tailscale.Auth.Method)
	}
	if cfg.Tailscale.Auth.APIKey != "tskey-abc" {
		t.Errorf("Auth.APIKey = %q, want tskey-abc", cfg.Tailscale.Auth.APIKey)
	}
	if cfg.Tailscale.Auth.OAuth.ClientID != "cid" {
		t.Errorf("OAuth.ClientID = %q, want cid", cfg.Tailscale.Auth.OAuth.ClientID)
	}
	if got := cfg.Tailscale.Auth.OAuth.Scopes; len(got) != 2 || got[0] != "all:read" || got[1] != "devices:read" {
		t.Errorf("OAuth.Scopes = %v, want [all:read devices:read]", got)
	}
	if cfg.Tailscale.HTTP.Timeout.D() != 45*time.Second {
		t.Errorf("HTTP.Timeout = %v, want 45s", cfg.Tailscale.HTTP.Timeout.D())
	}
	if cfg.Tailscale.HTTP.Retry.MaxAttempts != 7 {
		t.Errorf("Retry.MaxAttempts = %d, want 7", cfg.Tailscale.HTTP.Retry.MaxAttempts)
	}
	if cfg.Tailscale.HTTP.Retry.BaseDelay.D() != 250*time.Millisecond {
		t.Errorf("Retry.BaseDelay = %v, want 250ms", cfg.Tailscale.HTTP.Retry.BaseDelay.D())
	}
	if cfg.OTLP.Protocol != "grpc" {
		t.Errorf("OTLP.Protocol = %q, want grpc", cfg.OTLP.Protocol)
	}
	if cfg.OTLP.GrafanaCloud.InstanceID != "12345" {
		t.Errorf("GrafanaCloud.InstanceID = %q, want 12345", cfg.OTLP.GrafanaCloud.InstanceID)
	}
	if cfg.OTLP.Headers["X-Scope-OrgID"] != "tenant-1" {
		t.Errorf("Headers[X-Scope-OrgID] = %q, want tenant-1", cfg.OTLP.Headers["X-Scope-OrgID"])
	}
	if !cfg.OTLP.TLS.Insecure {
		t.Errorf("TLS.Insecure = false, want true")
	}
	if cfg.OTLP.TLS.CAFile != "/etc/ca.pem" {
		t.Errorf("TLS.CAFile = %q, want /etc/ca.pem", cfg.OTLP.TLS.CAFile)
	}
	if cfg.OTLP.MetricInterval.D() != 15*time.Second {
		t.Errorf("MetricInterval = %v, want 15s", cfg.OTLP.MetricInterval.D())
	}
	if cfg.Enrichment.CacheTTL.D() != 10*time.Minute {
		t.Errorf("Enrichment.CacheTTL = %v, want 10m", cfg.Enrichment.CacheTTL.D())
	}
	if !cfg.Cardinality.FlowIncludePorts {
		t.Errorf("Cardinality.FlowIncludePorts = false, want true")
	}
	if cfg.Cardinality.FlowNodeDims {
		t.Errorf("Cardinality.FlowNodeDims = true, want false")
	}

	// Collectors struct with per-collector fields.
	if cfg.Collectors.Devices.Enabled {
		t.Errorf("Collectors.Devices.Enabled = true, want false")
	}
	if cfg.Collectors.Devices.Interval.D() != 90*time.Second {
		t.Errorf("Devices.Interval = %v, want 90s", cfg.Collectors.Devices.Interval.D())
	}
	if !cfg.Collectors.Devices.CollectRoutes || !cfg.Collectors.Devices.CollectPosture {
		t.Errorf("Devices.CollectRoutes/Posture = %v/%v, want true/true",
			cfg.Collectors.Devices.CollectRoutes, cfg.Collectors.Devices.CollectPosture)
	}
	if cfg.Collectors.Flowlogs.Source != "stream" {
		t.Errorf("Flowlogs.Source = %q, want stream", cfg.Collectors.Flowlogs.Source)
	}
	if cfg.Collectors.Flowlogs.Lag.D() != 200*time.Second {
		t.Errorf("Flowlogs.Lag = %v, want 200s", cfg.Collectors.Flowlogs.Lag.D())
	}
	if cfg.Collectors.Flowlogs.LogMode != "per_record" {
		t.Errorf("Flowlogs.LogMode = %q, want per_record", cfg.Collectors.Flowlogs.LogMode)
	}
	if cfg.Collectors.Keys.ExpiryWarn.D() != 72*time.Hour {
		t.Errorf("Keys.ExpiryWarn = %v, want 72h", cfg.Collectors.Keys.ExpiryWarn.D())
	}
	if cfg.Checkpoint.Store != "file" {
		t.Errorf("Checkpoint.Store = %q, want file", cfg.Checkpoint.Store)
	}
	if cfg.Checkpoint.FilePath != "/tmp/cp.json" {
		t.Errorf("Checkpoint.FilePath = %q, want /tmp/cp.json", cfg.Checkpoint.FilePath)
	}
	if !cfg.Streaming.Enabled {
		t.Errorf("Streaming.Enabled = false, want true")
	}
	if cfg.Streaming.Listen != ":9000" {
		t.Errorf("Streaming.Listen = %q, want :9000", cfg.Streaming.Listen)
	}
	if cfg.Streaming.Decompress != "gzip" {
		t.Errorf("Streaming.Decompress = %q, want gzip", cfg.Streaming.Decompress)
	}
	if !cfg.Webhook.Enabled || cfg.Webhook.Secret != "wsecret" {
		t.Errorf("Webhook = %+v, want enabled with secret wsecret", cfg.Webhook)
	}
	if cfg.SelfObservability.Enabled {
		t.Errorf("SelfObservability.Enabled = true, want false")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := config.Load("/nonexistent/path/config.yaml"); err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}
