// Package config loads, env-expands, defaults, and validates the
// tailscale2otel YAML configuration into typed Go structs.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration document.
type Config struct {
	LogLevel          string                  `yaml:"log_level"`
	Tailscale         TailscaleConfig         `yaml:"tailscale"`
	OTLP              OTLPConfig              `yaml:"otlp"`
	Enrichment        EnrichmentConfig        `yaml:"enrichment"`
	Cardinality       CardinalityConfig       `yaml:"cardinality"`
	Collectors        Collectors              `yaml:"collectors"`
	Checkpoint        CheckpointConfig        `yaml:"checkpoint"`
	Streaming         StreamingConfig         `yaml:"streaming"`
	Webhook           WebhookConfig           `yaml:"webhook"`
	SelfObservability SelfObservabilityConfig `yaml:"self_observability"`
	Admin             AdminConfig             `yaml:"admin"`
}

// AdminConfig configures the optional always-on admin HTTP server that exposes
// liveness/readiness endpoints (/healthz, /readyz).
type AdminConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// TailscaleConfig holds Tailscale API connection settings.
type TailscaleConfig struct {
	Tailnet string              `yaml:"tailnet"`
	Auth    TailscaleAuth       `yaml:"auth"`
	HTTP    TailscaleHTTPConfig `yaml:"http"`
}

// TailscaleAuth selects and configures the Tailscale authentication method.
type TailscaleAuth struct {
	Method string      `yaml:"method"`
	OAuth  OAuthConfig `yaml:"oauth"`
	APIKey string      `yaml:"apikey"`
}

// OAuthConfig holds OAuth client-credentials settings.
type OAuthConfig struct {
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scopes       []string `yaml:"scopes"`
	TokenURL     string   `yaml:"token_url"`
}

// TailscaleHTTPConfig configures the HTTP client used for the Tailscale API.
type TailscaleHTTPConfig struct {
	Timeout Duration    `yaml:"timeout"`
	Retry   RetryConfig `yaml:"retry"`
	// RateLimit caps the global request rate (requests/second) across every
	// collector. Zero (the default) means unlimited.
	RateLimit float64 `yaml:"rate_limit"`
}

// RetryConfig configures exponential backoff retries.
type RetryConfig struct {
	MaxAttempts int      `yaml:"max_attempts"`
	BaseDelay   Duration `yaml:"base_delay"`
	MaxDelay    Duration `yaml:"max_delay"`
}

// OTLPConfig configures the OTLP exporter.
type OTLPConfig struct {
	Protocol       string             `yaml:"protocol"`
	Endpoint       string             `yaml:"endpoint"`
	GrafanaCloud   GrafanaCloudConfig `yaml:"grafana_cloud"`
	Headers        map[string]string  `yaml:"headers"`
	TLS            TLSConfig          `yaml:"tls"`
	MetricInterval Duration           `yaml:"metric_interval"`
}

// GrafanaCloudConfig holds Grafana Cloud OTLP credentials.
type GrafanaCloudConfig struct {
	InstanceID string `yaml:"instance_id"`
	Token      string `yaml:"token"`
}

// TLSConfig configures transport security for OTLP.
type TLSConfig struct {
	Insecure bool   `yaml:"insecure"`
	CAFile   string `yaml:"ca_file"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// EnrichmentConfig configures device-enrichment caching.
type EnrichmentConfig struct {
	CacheTTL Duration `yaml:"cache_ttl"`
}

// CardinalityConfig controls metric/label cardinality trade-offs.
type CardinalityConfig struct {
	FlowIncludePorts bool `yaml:"flow_include_ports"`
	FlowNodeDims     bool `yaml:"flow_node_dims"`
	CollapseExternal bool `yaml:"collapse_external"`
}

// Collectors groups the per-collector configurations.
type Collectors struct {
	Devices     CollectorConfig   `yaml:"devices"`
	Flowlogs    CollectorConfig   `yaml:"flowlogs"`
	Auditlogs   CollectorConfig   `yaml:"auditlogs"`
	Users       CollectorConfig   `yaml:"users"`
	Keys        CollectorConfig   `yaml:"keys"`
	Settings    CollectorConfig   `yaml:"settings"`
	Acl         CollectorConfig   `yaml:"acl"`
	Dns         CollectorConfig   `yaml:"dns"`
	NodeMetrics NodeMetricsConfig `yaml:"node_metrics"`
}

// NodeMetricsConfig configures the optional node-local metrics scraper, which
// scrapes a configured list of Prometheus-text /metrics endpoints (e.g.
// tailscaled per-node metrics) and re-emits them centrally. It is off by
// default and disabled when no targets are configured. Node identity is carried
// as the "instance" label, not as an OTEL Resource.
type NodeMetricsConfig struct {
	Enabled  bool                `yaml:"enabled"`
	Interval Duration            `yaml:"interval"`
	Timeout  Duration            `yaml:"timeout"`
	Targets  []NodeMetricsTarget `yaml:"targets"`
}

// NodeMetricsTarget is a single Prometheus-text endpoint to scrape. Instance
// overrides the default host:port "instance" label; Labels are passthrough
// attributes merged onto every series from this target.
type NodeMetricsTarget struct {
	URL      string            `yaml:"url"`
	Instance string            `yaml:"instance"`
	Labels   map[string]string `yaml:"labels"`
}

// CollectorConfig is the union of all per-collector options. Not every field
// applies to every collector; unused fields stay at their zero value.
type CollectorConfig struct {
	Enabled         bool     `yaml:"enabled"`
	Source          string   `yaml:"source"`
	Interval        Duration `yaml:"interval"`
	Lag             Duration `yaml:"lag"`
	InitialLookback Duration `yaml:"initial_lookback"`
	MaxWindow       Duration `yaml:"max_window"`
	LogMode         string   `yaml:"log_mode"`
	ExpiryWarn      Duration `yaml:"expiry_warn"`
	CollectRoutes   bool     `yaml:"collect_routes"`
	CollectPosture  bool     `yaml:"collect_posture"`
}

// CheckpointConfig configures high-water-mark persistence.
type CheckpointConfig struct {
	Store    string `yaml:"store"`
	FilePath string `yaml:"file_path"`
}

// StreamingConfig configures the HEC-style streaming receiver.
type StreamingConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	Path    string `yaml:"path"`
	Token   string `yaml:"token"`
	// PublicURL is the externally reachable URL Tailscale should POST logs to
	// (this receiver's public endpoint). Required only when AutoConfigure is on,
	// since it is the sink URL registered with Tailscale.
	PublicURL  string       `yaml:"public_url"`
	TLS        StreamingTLS `yaml:"tls"`
	Decompress string       `yaml:"decompress"`
	// AutoConfigure, when true, PUTs this receiver as a Splunk-HEC log-streaming
	// sink on startup (requires Enabled and PublicURL). Off by default.
	AutoConfigure bool `yaml:"auto_configure"`
}

// StreamingTLS configures TLS for the streaming receiver.
type StreamingTLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// WebhookConfig configures the inbound webhook receiver.
type WebhookConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	Path    string `yaml:"path"`
	Secret  string `yaml:"secret"`
}

// SelfObservabilityConfig toggles emitting the collector's own telemetry.
type SelfObservabilityConfig struct {
	Enabled bool `yaml:"enabled"`
}

// Load reads the YAML file at path, expands ${VAR}/$VAR references from the
// environment, applies defaults for unset fields, validates, and returns the
// resulting Config.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	expanded := os.Expand(string(raw), func(key string) string {
		v, _ := os.LookupEnv(key)
		return v
	})

	cfg := Default()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
