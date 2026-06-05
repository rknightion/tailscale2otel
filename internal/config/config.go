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
	Profiling         ProfilingConfig         `yaml:"profiling"`

	// unsetEnvRefs records ${VAR} references in the source file that were undefined
	// at load time (so they expanded to ""). Unexported, populated by Load, and
	// surfaced via Warnings so a typo'd or missing credential variable is flagged
	// instead of silently becoming an empty value.
	unsetEnvRefs []string
}

// AdminConfig configures the optional always-on admin HTTP server that exposes
// liveness/readiness endpoints (/healthz, /readyz).
type AdminConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	// LandingPage (default true) serves a human-readable landing page at "/" and
	// a machine-readable "/api/status.json" on the admin server.
	LandingPage bool `yaml:"landing_page"`
	// Auth optionally gates the status page and pprof behind a shared secret.
	Auth AdminAuth `yaml:"auth"`
}

// AdminAuth gates the status page ("/" and "/api/status.json") and the pprof
// handlers behind a shared secret. When Token is set, callers must present it as
// the HTTP Basic password OR as "Authorization: Bearer <token>". The /healthz
// and /readyz probes are never gated. Keep the token in an env var:
// token: "${ADMIN_TOKEN}".
type AdminAuth struct {
	Token Secret `yaml:"token"`
}

// ProfilingConfig configures continuous/on-demand profiling. Everything here is
// opt-in and off by default: net/http/pprof handlers (mounted on the admin
// server) and a Pyroscope push agent, plus the runtime mutex/block sampling
// knobs they depend on.
type ProfilingConfig struct {
	Pprof     ProfilingPprof     `yaml:"pprof"`
	Pyroscope ProfilingPyroscope `yaml:"pyroscope"`
	// MutexProfileFraction sets runtime.SetMutexProfileFraction (0 = disabled);
	// BlockProfileRate sets runtime.SetBlockProfileRate (0 = disabled). Both feed
	// the pprof/Pyroscope mutex+block profiles.
	MutexProfileFraction int `yaml:"mutex_profile_fraction"`
	BlockProfileRate     int `yaml:"block_profile_rate"`
}

// ProfilingPprof toggles the net/http/pprof debug handlers, which are mounted on
// the admin HTTP server (so it requires admin.enabled).
type ProfilingPprof struct {
	Enabled bool `yaml:"enabled"`
}

// ProfilingPyroscope configures the Pyroscope continuous-profiling push agent.
// When enabled it requires ServerAddress; the basic-auth/tenant fields cover
// Grafana Cloud Profiles and multi-tenant servers.
type ProfilingPyroscope struct {
	Enabled           bool              `yaml:"enabled"`
	ServerAddress     string            `yaml:"server_address"`
	BasicAuthUser     string            `yaml:"basic_auth_user"`
	BasicAuthPassword Secret            `yaml:"basic_auth_password"`
	TenantID          string            `yaml:"tenant_id"`
	UploadRate        Duration          `yaml:"upload_rate"`
	Tags              map[string]string `yaml:"tags"`
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
	APIKey Secret      `yaml:"apikey"`
}

// OAuthConfig holds OAuth client-credentials settings.
type OAuthConfig struct {
	ClientID     string   `yaml:"client_id"`
	ClientSecret Secret   `yaml:"client_secret"`
	Scopes       []string `yaml:"scopes"`
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
	Token      Secret `yaml:"token"`
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
	CacheTTL   Duration         `yaml:"cache_ttl"`
	ReverseDNS ReverseDNSConfig `yaml:"reverse_dns"`
}

// ReverseDNSConfig configures opt-in reverse-DNS (PTR) enrichment of EXTERNAL
// (non-Tailscale) flow addresses. When enabled, a resolved hostname replaces the
// "external" bucket / raw IP in tailscale.src.node / tailscale.dst.node on flow
// logs and metrics. Lookups are async and cached; the hot path never blocks.
type ReverseDNSConfig struct {
	Enabled bool `yaml:"enabled"`
	// Server is the resolver to query as "ip" or "ip:port" (default port 53). Empty
	// uses the system/container default resolver.
	Server      string   `yaml:"server"`
	Timeout     Duration `yaml:"timeout"`      // per-lookup timeout
	CacheTTL    Duration `yaml:"cache_ttl"`    // positive-result TTL
	NegativeTTL Duration `yaml:"negative_ttl"` // failed-lookup TTL
	MaxEntries  int      `yaml:"max_entries"`  // cache size bound
}

// CardinalityConfig controls metric/label cardinality trade-offs.
type CardinalityConfig struct {
	// FlowMetricsMode selects which flow metric families to emit:
	//   "rollup" (default) — bounded top-N *.rollup families: the busiest
	//     source/destination node pairs by bytes are kept (flow_rollup_top_n) and the
	//     remainder folds into an __other__ series per transport/traffic_type/service
	//     so totals are preserved. Carries no L4 ports. Lowest cardinality; also adds
	//     the per-source-node tailscale.network.unique.* gauges.
	//   "all"  — per-connection raw tailscale.network.io/packets, shaped by the
	//     toggles below (highest fidelity, highest cardinality).
	//   "both" — emit BOTH families (≈2x series; summing them double-counts — see Warnings).
	// The raw and rollup families share semantic conventions; the rollup attribute
	// keys are a subset (no ports) plus the __other__ sentinel value.
	FlowMetricsMode string `yaml:"flow_metrics_mode"`
	// FlowIncludePorts is the legacy "both ports" toggle for flow METRICS; it is
	// OR'd with FlowSourcePort/FlowDestinationPort so existing configs keep working.
	// It applies to the raw families (mode all/both); the rollup never carries ports.
	FlowIncludePorts bool `yaml:"flow_include_ports"`
	// FlowSourcePort / FlowDestinationPort independently add source.port /
	// destination.port to flow METRICS (both default false; flow LOGS always carry
	// both ports regardless).
	FlowSourcePort      bool `yaml:"flow_source_port"`
	FlowDestinationPort bool `yaml:"flow_destination_port"`
	// FlowDestinationService adds tailscale.dst.service (the IANA service name for
	// the destination port+transport, e.g. tcp/443 -> "https") to flow METRICS as a
	// bounded, low-cardinality stand-in for the destination port. Default false;
	// flow LOGS always carry it when the port maps to a known service.
	FlowDestinationService bool `yaml:"flow_destination_service"`
	FlowNodeDims           bool `yaml:"flow_node_dims"`
	CollapseExternal       bool `yaml:"collapse_external"`
	// DevicePerEntity/UserPerEntity/KeyPerEntity (default true) gate the
	// per-entity gauges in the devices/users/keys collectors. When false, only
	// the low-cardinality aggregate *.count rollups are emitted (the per-entity
	// gauges, one series per device/user/key, are dropped).
	DevicePerEntity bool `yaml:"device_per_entity"`
	UserPerEntity   bool `yaml:"user_per_entity"`
	KeyPerEntity    bool `yaml:"key_per_entity"`
	// WebhookPerEntity (default true) gates the per-endpoint
	// tailscale.webhook_endpoint.subscriptions gauge; false keeps only the
	// aggregate tailscale.webhook_endpoints.count.
	WebhookPerEntity bool `yaml:"webhook_per_entity"`
	// MetricLimit is the hard per-instrument cardinality limit: the maximum number
	// of distinct attribute sets (series) a single metric may emit per collection
	// cycle. Beyond it the OTLP SDK collapses further series into one
	// otel_metric_overflow series (silent loss of detail), so size it above your
	// busiest flow-metric cardinality. Cardinality is primarily shaped by the
	// toggles above (ports/node-dims/collapse-external); this is the safety cap.
	// Default 10000; 0 or negative disables the limit (unlimited).
	MetricLimit int `yaml:"metric_limit"`
}

// Collectors groups the per-collector configurations.
type Collectors struct {
	Devices             CollectorConfig   `yaml:"devices"`
	Flowlogs            CollectorConfig   `yaml:"flowlogs"`
	Auditlogs           CollectorConfig   `yaml:"auditlogs"`
	Users               CollectorConfig   `yaml:"users"`
	Keys                CollectorConfig   `yaml:"keys"`
	Settings            CollectorConfig   `yaml:"settings"`
	Acl                 CollectorConfig   `yaml:"acl"`
	Dns                 CollectorConfig   `yaml:"dns"`
	Contacts            CollectorConfig   `yaml:"contacts"`
	Webhooks            CollectorConfig   `yaml:"webhooks"`
	PostureIntegrations CollectorConfig   `yaml:"posture_integrations"`
	NodeMetrics         NodeMetricsConfig `yaml:"node_metrics"`
}

// NodeMetricsConfig configures the optional node-local metrics scraper, which
// scrapes a configured list of Prometheus-text /metrics endpoints (e.g.
// tailscaled per-node metrics) and re-emits them centrally. It is off by
// default and disabled when no targets are configured. Node identity is carried
// as the "instance" label, not as an OTEL Resource.
type NodeMetricsConfig struct {
	Enabled   bool                 `yaml:"enabled"`
	Interval  Duration             `yaml:"interval"`
	Timeout   Duration             `yaml:"timeout"`
	Targets   []NodeMetricsTarget  `yaml:"targets"`
	Discovery NodeMetricsDiscovery `yaml:"discovery"`

	// Scrape limits bound memory and telemetry cardinality per target.
	MaxResponseBytes int64 `yaml:"max_response_bytes"` // maximum response bytes read from one scrape
	MaxSamples       int   `yaml:"max_samples"`        // maximum valid samples forwarded from one scrape

	// Passthrough filters on the FORWARDED Prometheus samples. They never affect
	// tailscale.node.up or the discovery.* gauges. A zero value means no filtering.
	MetricAllow []string `yaml:"metric_allow"` // anchored regex on the forwarded metric NAME; if non-empty, a name must match one to be forwarded
	MetricDeny  []string `yaml:"metric_deny"`  // anchored regex; a name matching any is dropped (applied after allow)
	DropLabels  []string `yaml:"drop_labels"`  // label keys stripped from every forwarded series (the `instance` label is never dropped)
}

// NodeMetricsDiscovery configures DYNAMIC scrape-target discovery from the
// Tailscale devices API. When enabled, the live device inventory is polled on
// this block's own Interval (default 5m, independent of the scrape Interval) and
// each matching device becomes a scrape target; discovered targets are UNIONED
// (deduped by URL) with the static Targets list, so existing static-only configs
// are unaffected. Reachability is reported by tailscale.node.up=0 for any node
// the scraper cannot reach (no ACL parsing is performed).
type NodeMetricsDiscovery struct {
	Enabled  bool     `yaml:"enabled"`
	Interval Duration `yaml:"interval"`

	// Metrics-endpoint shape applied to every discovered device.
	Scheme string `yaml:"scheme"` // "http" (default) | "https"
	Port   int    `yaml:"port"`   // default 5252 (tailscaled client metrics)
	Path   string `yaml:"path"`   // default "/metrics"

	// Filters.
	MaxTargets      int      `yaml:"max_targets"`      // maximum discovered targets per refresh
	OnlineOnly      bool     `yaml:"online_only"`      // default true: only connectedToControl devices
	ExcludeExternal bool     `yaml:"exclude_external"` // default true: skip shared/external devices
	IncludeTags     []string `yaml:"include_tags"`     // empty = match all; any-match
	ExcludeTags     []string `yaml:"exclude_tags"`     // wins over include_tags

	// Address + instance selection.
	AddressOrder   string `yaml:"address_order"`   // "ipv4" (default) | "ipv6" (preferred family; falls back to the other)
	InstanceSource string `yaml:"instance_source"` // node identity label: "address" (default, host:port) | "name" (MagicDNS short name, unique) | "hostname" (OS hostname; NOT unique — collisions like "localhost" are disambiguated by address + WARN)

	// Passthrough labels merged onto each discovered target's series, for
	// join-ability with tailscale.device.* (host.name/host.id, tailscale.tags).
	IncludeHostLabels bool `yaml:"include_host_labels"` // default true
	IncludeTagsLabel  bool `yaml:"include_tags_label"`  // default true
}

// NodeMetricsTarget is a single Prometheus-text endpoint to scrape. Instance
// overrides the default host:port "instance" label; Labels are passthrough
// attributes merged onto every series from this target.
//
// The optional auth/TLS fields cover PROXIED/HTTPS targets; native tailscaled
// /metrics endpoints are plain HTTP with no auth/TLS, so leaving them unset keeps
// the scrape a plain GET. BearerTokenFile (read fresh each scrape) takes
// precedence over BearerToken.
type NodeMetricsTarget struct {
	URL             string                `yaml:"url"`
	Instance        string                `yaml:"instance"`
	Labels          map[string]string     `yaml:"labels"`
	BearerToken     Secret                `yaml:"bearer_token"`
	BearerTokenFile string                `yaml:"bearer_token_file"`
	Headers         map[string]string     `yaml:"headers"`
	TLS             *NodeMetricsTargetTLS `yaml:"tls"`
}

// NodeMetricsTargetTLS is the optional per-target TLS trust/identity for HTTPS
// node-metrics targets. InsecureSkipVerify defaults to false (a footgun guard).
type NodeMetricsTargetTLS struct {
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	CAFile             string `yaml:"ca_file"`
	CertFile           string `yaml:"cert_file"`
	KeyFile            string `yaml:"key_file"`
	ServerName         string `yaml:"server_name"`
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
	// PostureLogMode controls the tailscale.device.posture LOG (devices collector,
	// requires collect_posture): "changes" (default) logs a device only when its
	// posture changes since the last scrape — a full baseline dump on the first
	// scrape, then deltas; "always" logs every scrape (the prior behavior); "off"
	// suppresses the log entirely. The posture info-gauge METRIC is emitted every
	// scrape regardless of this setting.
	PostureLogMode string `yaml:"posture_log_mode"`
	// AttributeNamespaces lists the device posture-attribute namespace prefixes (the
	// part before ":" in a posture key, e.g. "intune", "ip") promoted to the
	// tailscale.device.attribute{,.info} metrics (devices collector; requires
	// collect_posture, which fetches the attributes — no extra API calls). Default:
	// the integration namespaces plus ip. The sentinel ["*"] promotes every namespace
	// present (including node and custom). An explicit empty list ([]) disables the
	// attribute metrics; node:* is excluded by default (already on the curated posture
	// gauge) and custom:* by default (operator-defined, potentially unbounded values).
	AttributeNamespaces []string `yaml:"attribute_namespaces"`
	// MaxLogRecordsPerWindow caps flow LOG records emitted per poll window
	// (flowlogs only; 0 = unlimited). Excess is counted into
	// tailscale.network.flow.logs_dropped; metrics are never capped.
	MaxLogRecordsPerWindow int `yaml:"max_log_records_per_window"`
	// FlowRollupTopN bounds the number of busiest source/destination node pairs the
	// flow-metrics rollup keeps per flush (flowlogs only; only used when
	// cardinality.flow_metrics_mode is rollup or both). Pairs beyond it fold into the
	// __other__ series. 0 selects the default (500).
	FlowRollupTopN int `yaml:"flow_rollup_top_n"`
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
	Token   Secret `yaml:"token"`
	// PublicURL is the externally reachable URL Tailscale should POST logs to
	// (this receiver's public endpoint). Required only when AutoConfigure is on,
	// since it is the sink URL registered with Tailscale.
	PublicURL  string       `yaml:"public_url"`
	TLS        StreamingTLS `yaml:"tls"`
	Decompress string       `yaml:"decompress"`
	// AutoConfigure, when true, PUTs this receiver as a Splunk-HEC log-streaming
	// sink on startup (requires Enabled and PublicURL). Off by default.
	AutoConfigure bool `yaml:"auto_configure"`
	// MaxBodyBytes caps the DECOMPRESSED request body; an over-cap POST is
	// rejected with 413 + rejected{reason=too_large} so a huge or zip-bomb body
	// cannot OOM the receiver. 0 selects a 64 MiB default; negative disables it.
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
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
	Secret  Secret `yaml:"secret"`
	// Tolerance is the maximum age of a webhook's signed timestamp before it is
	// rejected as a replay. Tailscale signs "<unix>.<body>", so this bounds how
	// long a captured, validly-signed delivery can be replayed. 0 disables the
	// check; defaults to 5m.
	Tolerance Duration `yaml:"tolerance"`
	// DedupAuditEvents, when true, shares a best-effort cross-source de-dup set
	// with the audit processor so a change reported by BOTH a webhook and the
	// audit logs is counted once. Off by default (the type<->action mapping is
	// best-effort; see internal/webhook crossKey). See also S4-11.
	DedupAuditEvents bool `yaml:"dedup_audit_events"`
}

// SelfObservabilityConfig toggles emitting the collector's own telemetry.
type SelfObservabilityConfig struct {
	Enabled bool `yaml:"enabled"`
	// InstanceID sets the service.instance.id resource attribute so multiple
	// instances of the exporter are distinguishable in the backend. When empty
	// it falls back to the host name (see internal/app instanceID). Supports
	// ${ENV} expansion, e.g. "${POD_NAME}".
	InstanceID string `yaml:"instance_id"`
}

// Load reads the YAML file at path, expands ${VAR}/$VAR references from the
// environment, applies defaults for unset fields, validates, and returns the
// resulting Config.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var unset []string
	seen := map[string]struct{}{}
	expanded := os.Expand(string(raw), func(key string) string {
		v, ok := os.LookupEnv(key)
		if !ok {
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				unset = append(unset, key)
			}
		}
		return v
	})

	cfg := Default()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.unsetEnvRefs = unset

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
