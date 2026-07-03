// Package config loads, defaults, and validates the tailscale2otel
// configuration into typed Go structs.
//
// Configuration is layered, lowest precedence first: built-in defaults
// (Default) -> an optional YAML file -> environment variables. Every field is
// settable via an environment variable named with the TS2OTEL_ prefix and "__"
// as the nesting delimiter (single underscores inside a name are preserved):
//
//	TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID -> tailscale.auth.oauth.client_id
//	TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL    -> collectors.flowlogs.interval
//
// The env layer overrides the file, so secrets live in environment variables
// and never need to appear in the YAML. The file is optional: with no -config
// path the process runs from defaults + environment alone (handy for
// containers).
package config

import (
	"fmt"
	"os"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	env "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// EnvPrefix is the prefix for every configuration environment variable.
const EnvPrefix = "TS2OTEL_"

// keyDelim is koanf's internal key-path delimiter; envNestDelim is the token
// that separates nesting levels in an environment-variable name (so a single
// underscore within a level, e.g. client_id, is preserved).
const (
	keyDelim     = "."
	envNestDelim = "__"
)

// Config is the root configuration document.
type Config struct {
	LogLevel  string          `yaml:"log_level"`
	Provider  string          `yaml:"provider"` // "tailscale" (default) | "headscale"
	Tailscale TailscaleConfig `yaml:"tailscale"`
	// Tailnets is the optional multi-tailnet list (MSP mode). When non-empty the
	// instance observes every listed tailnet; it is mutually exclusive with an
	// explicit single tailscale.tailnet (Validate errors if both name a tailnet).
	// Each entry is self-contained (its own name + auth + http). FILE-ONLY: like
	// collectors.node_metrics.targets, a list-of-structs is not settable via flat
	// TS2OTEL_* env vars — use a config file for multi-tailnet.
	Tailnets          []TailnetConfig         `yaml:"tailnets"`
	Headscale         HeadscaleConfig         `yaml:"headscale"`
	OTLP              OTLPConfig              `yaml:"otlp"`
	Enrichment        EnrichmentConfig        `yaml:"enrichment"`
	Cardinality       CardinalityConfig       `yaml:"cardinality"`
	Collectors        Collectors              `yaml:"collectors"`
	Checkpoint        CheckpointConfig        `yaml:"checkpoint"`
	Streaming         StreamingConfig         `yaml:"streaming"`
	Webhook           WebhookConfig           `yaml:"webhook"`
	SelfObservability SelfObservabilityConfig `yaml:"self_observability"`
	PIIFilter         PIIFilterConfig         `yaml:"pii_filter"`
	Admin             AdminConfig             `yaml:"admin"`
	Prometheus        PrometheusConfig        `yaml:"prometheus"`
	Profiling         ProfilingConfig         `yaml:"profiling"`
	Tracing           TracingConfig           `yaml:"tracing"`
	VersionChecks     VersionChecksConfig     `yaml:"version_checks"`

	// unknownEnv records TS2OTEL_* environment variables that did not map to any
	// known config key (a likely typo — they were ignored). Unexported, populated
	// by Load, surfaced via Warnings.
	unknownEnv []string

	// configFileWarning is a load-time advisory about the config file itself
	// (currently: loose permissions). Surfaced by Warnings(), like unknownEnv.
	configFileWarning string
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
// TS2OTEL_ADMIN__AUTH__TOKEN.
type AdminAuth struct {
	Token Secret `yaml:"token"`
}

// PrometheusConfig configures the optional Prometheus pull endpoint (GET /metrics)
// on a DEDICATED listener, independent of the admin server. Off by default. When
// enabled it runs an additional metric.Reader alongside OTLP push, so both export
// paths are active at once (backwards-compat for Prometheus scrapers).
type PrometheusConfig struct {
	Enabled bool           `yaml:"enabled"`
	Listen  string         `yaml:"listen"`
	Auth    PrometheusAuth `yaml:"auth"`
}

// PrometheusAuth optionally gates /metrics behind a shared secret presented as the
// HTTP Basic password OR "Authorization: Bearer <token>". Empty = open (bind to a
// loopback/tailnet address or rely on network controls). Keep the token in an env
// var: TS2OTEL_PROMETHEUS__AUTH__TOKEN.
type PrometheusAuth struct {
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

// VersionChecksConfig configures the optional outbound "is a newer release
// available?" checks. Both sub-checks make external HTTPS calls and are
// fail-open (a failed/blocked fetch silently emits nothing). cache_ttl bounds
// how often the upstream endpoints are hit.
type VersionChecksConfig struct {
	Self     VersionCheckSelf    `yaml:"self"`
	Devices  VersionCheckDevices `yaml:"devices"`
	CacheTTL Duration            `yaml:"cache_ttl"`
	Timeout  Duration            `yaml:"timeout"`
}

// VersionCheckSelf gates the self update-available gauge (tailscale2otel.update_available),
// comparing the running build to the latest tailscale2otel GitHub release.
type VersionCheckSelf struct {
	Enabled bool `yaml:"enabled"`
}

// VersionCheckDevices gates the per-device/fleet Tailscale-client version-skew
// metrics, comparing each device's client version to the latest Tailscale stable.
type VersionCheckDevices struct {
	Enabled                bool `yaml:"enabled"`
	OutdatedMinorThreshold int  `yaml:"outdated_minor_threshold"`
}

// HeadscaleConfig holds Headscale control-plane connection settings (used when
// provider: headscale). Auth is a Bearer API key; keep it in env (TS2OTEL_*).
type HeadscaleConfig struct {
	URL    string              `yaml:"url"`
	APIKey Secret              `yaml:"api_key"`
	HTTP   TailscaleHTTPConfig `yaml:"http"` // reuse the same timeout/retry/rate_limit shape
}

// TailscaleConfig holds Tailscale API connection settings.
type TailscaleConfig struct {
	Tailnet string              `yaml:"tailnet"`
	Auth    TailscaleAuth       `yaml:"auth"`
	HTTP    TailscaleHTTPConfig `yaml:"http"`
}

// TailnetConfig is one entry in the multi-tailnet list. It mirrors the
// connection-bearing fields of TailscaleConfig but names the tailnet explicitly.
type TailnetConfig struct {
	Name string              `yaml:"name"`
	Auth TailscaleAuth       `yaml:"auth"`
	HTTP TailscaleHTTPConfig `yaml:"http"`
}

// ResolvedTailnet is the normalized, per-tailnet connection config the app layer
// iterates. Both single mode (one tailscale: block) and multi mode (a tailnets:
// list) collapse to a []ResolvedTailnet via ResolvedTailnets.
type ResolvedTailnet struct {
	Name string
	Auth TailscaleAuth
	HTTP TailscaleHTTPConfig
}

// ResolvedTailnets normalizes the single tailscale: block OR the tailnets: list
// into the per-tailnet list the app fans out over. The list wins when present
// (Validate rejects an explicit single tailnet alongside it). Under provider:
// headscale the result is empty (Headscale has no tailnet fan-out in v1).
//
// Each tailnets[] entry's HTTP block is backfilled field-by-field with the
// precedence entry > top-level tailscale.http > Default().Tailscale.HTTP, so an
// entry that omits http: still gets real retry/timeout defaults (a zero
// MaxAttempts otherwise clamps to 1 in tsapi, silently disabling retries — #104)
// AND the top-level tailscale.http block acts as fleet-wide policy for the list
// (which is also how TS2OTEL_TAILSCALE__HTTP__* reaches multi-tailnet clients).
func (c *Config) ResolvedTailnets() []ResolvedTailnet {
	if c.Provider == "headscale" {
		return nil
	}
	if len(c.Tailnets) > 0 {
		// Effective fleet base: the top-level tailscale.http block with any zero
		// field filled from the built-in defaults, so backfill works even when the
		// top-level block is entirely unset in multi mode.
		base := mergeHTTPDefaults(c.Tailscale.HTTP, Default().Tailscale.HTTP)
		out := make([]ResolvedTailnet, len(c.Tailnets))
		for i, t := range c.Tailnets {
			out[i] = ResolvedTailnet{
				Name: t.Name,
				Auth: t.Auth,
				HTTP: mergeHTTPDefaults(t.HTTP, base),
			}
		}
		return out
	}
	return []ResolvedTailnet{{
		Name: c.Tailscale.Tailnet,
		Auth: c.Tailscale.Auth,
		HTTP: c.Tailscale.HTTP,
	}}
}

// mergeHTTPDefaults returns x with each zero-valued HTTP field taken from base.
// A zero RateLimit is genuinely "unlimited" and indistinguishable from unset, so
// it too inherits base (letting fleet-wide policy set on tailscale.http apply).
func mergeHTTPDefaults(x, base TailscaleHTTPConfig) TailscaleHTTPConfig {
	if x.Timeout <= 0 {
		x.Timeout = base.Timeout
	}
	if x.Retry.MaxAttempts <= 0 {
		x.Retry.MaxAttempts = base.Retry.MaxAttempts
	}
	if x.Retry.BaseDelay <= 0 {
		x.Retry.BaseDelay = base.Retry.BaseDelay
	}
	if x.Retry.MaxDelay <= 0 {
		x.Retry.MaxDelay = base.Retry.MaxDelay
	}
	if x.RateLimit == 0 {
		x.RateLimit = base.RateLimit
	}
	return x
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
	// AcknowledgeCardinality silences the startup advisory that fires when
	// reverse_dns.enabled=true AND cardinality.flow.node_dims=true (the only
	// combination where PTR names inflate flow-METRIC cardinality). Set to true
	// once you have sized cardinality.metric_limit for the added per-external-IP
	// series — it is purely an acknowledgement and does not change emission.
	AcknowledgeCardinality bool `yaml:"acknowledge_cardinality"`
}

// CardinalityConfig controls metric/label cardinality trade-offs. The two big
// knob groups are nested: Flow (the flow-metric shaping toggles) and PerEntity
// (whether to emit one gauge series per device/user/key/... or only the
// low-cardinality aggregate counts).
type CardinalityConfig struct {
	// MetricLimit is the hard per-instrument cardinality limit: the maximum number
	// of distinct attribute sets (series) a single metric may emit per collection
	// cycle. Beyond it the OTLP SDK collapses further series into one
	// otel_metric_overflow series (silent loss of detail), so size it above your
	// busiest flow-metric cardinality. Cardinality is primarily shaped by the
	// Flow toggles; this is the safety cap. Default 10000; 0 or negative disables
	// the limit (unlimited).
	MetricLimit int `yaml:"metric_limit"`
	// DerpRegionRollup (default true) gates the tailnet-wide per-DERP-region
	// rollup gauges (tailscale.derp.region.*) emitted by the devices collector.
	DerpRegionRollup bool `yaml:"derp_region_rollup"`
	// SubnetRouteRollup (default true) gates the per-CIDR
	// tailscale.subnet_routes.routers redundancy gauge (one series per subnet
	// CIDR). The fleet exit/subnet count aggregates are emitted regardless.
	SubnetRouteRollup bool `yaml:"subnet_route_rollup"`
	// Flow shapes the flow-metric families and their attributes.
	Flow FlowCardinality `yaml:"flow"`
	// PerEntity gates the per-entity gauges of the inventory collectors.
	PerEntity PerEntityCardinality `yaml:"per_entity"`
}

// FlowCardinality shapes the flow-metric families and the attributes carried on
// them. Flow LOGS are unaffected by these toggles (they always carry full
// detail); these knobs only bound the cardinality of flow METRICS.
type FlowCardinality struct {
	// MetricsMode selects which flow metric families to emit:
	//   "rollup" (default) — bounded top-N *.rollup families: the busiest
	//     source/destination node pairs by bytes are kept (RollupTopN) and the
	//     remainder folds into an __other__ series per transport/traffic_type/service
	//     so totals are preserved. Carries no L4 ports. Lowest cardinality; also adds
	//     the per-source-node tailscale.network.unique.* gauges.
	//   "all"  — per-connection raw tailscale.network.io/packets, shaped by the
	//     toggles below (highest fidelity, highest cardinality).
	//   "both" — emit BOTH families (≈2x series; summing them double-counts — see Warnings).
	MetricsMode string `yaml:"metrics_mode"`
	// RollupTopN bounds the number of busiest source/destination node pairs the
	// flow-metrics rollup keeps per flush (only used when MetricsMode is rollup or
	// both). Pairs beyond it fold into the __other__ series. 0 selects the default
	// (500).
	RollupTopN int `yaml:"rollup_top_n"`
	// SourcePort / DestinationPort independently add source.port / destination.port
	// to flow METRICS (both default false; flow LOGS always carry both ports).
	SourcePort      bool `yaml:"source_port"`
	DestinationPort bool `yaml:"destination_port"`
	// DestinationService adds tailscale.dst.service (the IANA service name for the
	// destination port+transport, e.g. tcp/443 -> "https") to flow METRICS as a
	// bounded, low-cardinality stand-in for the destination port. Default false.
	DestinationService bool `yaml:"destination_service"`
	// NodeDims (default true) includes the src/dst device names on flow metrics.
	NodeDims bool `yaml:"node_dims"`
	// CollapseExternal (default true) buckets unresolved IPs as external/unknown.
	CollapseExternal bool `yaml:"collapse_external"`
	// ExitNodeAttribution (default true) emits the bounded
	// tailscale.exit_node.io/packets counters attributing exit traffic to the
	// relaying node. Bounded by exit-node count; independent of MetricsMode.
	ExitNodeAttribution bool `yaml:"exit_node_attribution"`
}

// PerEntityCardinality gates the per-entity gauges of the inventory collectors.
// When a toggle is false, only the low-cardinality aggregate *.count rollup is
// emitted (the per-entity gauges, one series per device/user/key/..., are
// dropped). All default true.
type PerEntityCardinality struct {
	Device  bool `yaml:"device"`
	User    bool `yaml:"user"`
	Key     bool `yaml:"key"`
	Webhook bool `yaml:"webhook"`
	Service bool `yaml:"service"`
}

// Collectors groups the per-collector configurations. Each collector exposes
// only the fields that apply to it: the inventory snapshots take just
// enabled+interval (SimpleCollector); the two log collectors add a source and
// windowing fields; devices/keys/services have their own extras.
type Collectors struct {
	Devices             DevicesCollector   `yaml:"devices"`
	Flowlogs            FlowlogsCollector  `yaml:"flowlogs"`
	Auditlogs           AuditlogsCollector `yaml:"auditlogs"`
	Users               SimpleCollector    `yaml:"users"`
	Keys                KeysCollector      `yaml:"keys"`
	Settings            SimpleCollector    `yaml:"settings"`
	Acl                 SimpleCollector    `yaml:"acl"`
	Dns                 SimpleCollector    `yaml:"dns"`
	Contacts            SimpleCollector    `yaml:"contacts"`
	Webhooks            SimpleCollector    `yaml:"webhooks"`
	PostureIntegrations SimpleCollector    `yaml:"posture_integrations"`
	LogStream           SimpleCollector    `yaml:"log_stream"`
	Services            ServicesCollector  `yaml:"services"`
	NodeMetrics         NodeMetricsConfig  `yaml:"node_metrics"`
}

// SimpleCollector is a point-in-time inventory collector: it just polls a
// snapshot on its Interval.
type SimpleCollector struct {
	Enabled  bool     `yaml:"enabled"`
	Interval Duration `yaml:"interval"`
}

// DevicesCollector configures the devices collector. Besides the snapshot
// interval it gates the optional routes/posture fetches and the posture log.
type DevicesCollector struct {
	Enabled        bool     `yaml:"enabled"`
	Interval       Duration `yaml:"interval"`
	CollectRoutes  bool     `yaml:"collect_routes"`
	CollectPosture bool     `yaml:"collect_posture"`
	// CollectDeviceInvites fetches each device's outstanding share invites
	// (GET /device/{id}/device-invites — one API call per device, N+1) and emits
	// the tailscale.device_invites.count aggregate. Requires the
	// device_invites:read OAuth scope (covered by the broad all:read scope).
	// Per-device fetch failures are non-fatal. Default true.
	CollectDeviceInvites bool `yaml:"collect_device_invites"`
	// PostureLogMode controls the tailscale.device.posture LOG (requires
	// collect_posture): "changes" (default) logs a device only when its posture
	// changes since the last scrape — a full baseline dump on the first scrape,
	// then deltas; "always" logs every scrape; "off" suppresses the log. The
	// posture info-gauge METRIC is emitted every scrape regardless.
	PostureLogMode string `yaml:"posture_log_mode"`
	// AttributeNamespaces lists the device posture-attribute namespace prefixes (the
	// part before ":" in a posture key, e.g. "intune", "ip") promoted to the
	// tailscale.device.attribute{,.info} metrics (requires collect_posture). The
	// sentinel ["*"] promotes every namespace present; an explicit empty list ([])
	// disables the attribute metrics.
	AttributeNamespaces []string `yaml:"attribute_namespaces"`
	// CollectConnectivity (default true) gates the B3 connectivity signals
	// (hard_nat/endpoints/direct_capable/udp/ipv6 + fleet rollups). No extra API
	// calls — read from the rich device payload already fetched.
	CollectConnectivity bool `yaml:"collect_connectivity"`
	// CollectTagRollup (default true) gates the tailscale.devices.by_tag
	// distribution gauge (one series per ACL tag). When false, only the other
	// fleet-hygiene aggregates (untagged/ephemeral/by_version/key_expiry) emit.
	CollectTagRollup bool `yaml:"collect_tag_rollup"`
	// TagRollupLimit caps the number of distinct tag series on
	// tailscale.devices.by_tag: the busiest TagRollupLimit tags (by device count)
	// keep their own series; the rest fold into a single tailscale.tag="__other__"
	// series so totals are preserved. Default 50; 0 or negative = unlimited.
	TagRollupLimit int `yaml:"tag_rollup_limit"`
}

// FlowlogsCollector configures the network-flow-logs collector. Source selects
// the ingestion path (poll/stream/both); the windowing fields apply only when
// polling.
type FlowlogsCollector struct {
	Enabled         bool     `yaml:"enabled"`
	Source          string   `yaml:"source"`
	Interval        Duration `yaml:"interval"`         // poll only
	Lag             Duration `yaml:"lag"`              // poll only
	InitialLookback Duration `yaml:"initial_lookback"` // poll only
	MaxWindow       Duration `yaml:"max_window"`       // poll only
	// LogMode sets the per-connection/per-record/off log detail (applies to poll
	// AND stream).
	LogMode string `yaml:"log_mode"`
	// MaxLogRecordsPerWindow caps flow LOG records emitted per poll window (0 =
	// unlimited). Excess is counted into tailscale.network.flow.logs_dropped;
	// metrics are never capped.
	MaxLogRecordsPerWindow int `yaml:"max_log_records_per_window"`
}

// AuditlogsCollector configures the configuration/audit-events collector. Source
// selects the ingestion path; the windowing fields apply only when polling.
type AuditlogsCollector struct {
	Enabled         bool     `yaml:"enabled"`
	Source          string   `yaml:"source"`
	Interval        Duration `yaml:"interval"`         // poll only
	Lag             Duration `yaml:"lag"`              // poll only
	InitialLookback Duration `yaml:"initial_lookback"` // poll only
	MaxWindow       Duration `yaml:"max_window"`       // poll only
}

// KeysCollector configures the keys collector. ExpiryWarn sets how far ahead of
// a key's expiry the WARN log fires.
type KeysCollector struct {
	Enabled    bool     `yaml:"enabled"`
	Interval   Duration `yaml:"interval"`
	ExpiryWarn Duration `yaml:"expiry_warn"`
}

// ServicesCollector configures the Tailscale Services (VIP) collector.
// CollectHosts adds per-service backing-host detail — one extra API call per
// service (N+1). Off by default.
type ServicesCollector struct {
	Enabled      bool     `yaml:"enabled"`
	Interval     Duration `yaml:"interval"`
	CollectHosts bool     `yaml:"collect_hosts"`
}

// NodeMetricsConfig configures the optional node-local metrics scraper, which
// scrapes a configured list of Prometheus-text /metrics endpoints (e.g.
// tailscaled per-node metrics) and re-emits them centrally. It is off by
// default and disabled when no targets are configured. Node identity is carried
// as a label, not as an OTEL Resource.
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
	// audit logs is counted once. Off by default.
	DedupAuditEvents bool `yaml:"dedup_audit_events"`
}

// TracingConfig configures the OTEL traces pillar. Off by default; reuses otlp.*
// for the endpoint/protocol/headers/TLS.
type TracingConfig struct {
	Enabled    bool    `yaml:"enabled"`
	Sampler    string  `yaml:"sampler"`     // always_on|always_off|traceidratio|parentbased_always_on|parentbased_traceidratio
	SamplerArg float64 `yaml:"sampler_arg"` // ratio in [0,1] for the *traceidratio samplers
}

// PIIFilterConfig controls which PII / identifier categories are emitted.
// All categories default to true (emitted); set a category to false to drop
// those identifiers from metrics and logs at runtime (opt-out redaction).
type PIIFilterConfig struct {
	Emails           bool `yaml:"emails"`
	UserDisplayNames bool `yaml:"user_display_names"`
	UserIDs          bool `yaml:"user_ids"`
	Hostnames        bool `yaml:"hostnames"`
	NodeIDs          bool `yaml:"node_ids"`
	TailscaleIPs     bool `yaml:"tailscale_ips"`
	InternalIPs      bool `yaml:"internal_ips"`
	ExternalIPs      bool `yaml:"external_ips"`
	ServiceAddrs     bool `yaml:"service_addrs"`
	EndpointPaths    bool `yaml:"endpoint_paths"`
	NetworkTopology  bool `yaml:"network_topology"`
	TailnetName      bool `yaml:"tailnet_name"`
	FreeTextDetails  bool `yaml:"free_text_details"`
}

// SelfObservabilityConfig toggles emitting the collector's own telemetry.
type SelfObservabilityConfig struct {
	Enabled bool `yaml:"enabled"`
	// InstanceID sets the service.instance.id resource attribute so multiple
	// instances of the exporter are distinguishable in the backend. When empty it
	// falls back to the host name (see internal/app instanceID). Set it from the
	// environment, e.g. TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID=$POD_NAME.
	InstanceID string `yaml:"instance_id"`
}

// Load builds the configuration by layering, lowest precedence first: built-in
// defaults, an optional YAML file at path (skipped when path is ""), and
// TS2OTEL_* environment variables. The merged result is validated before it is
// returned. A non-empty path that cannot be read is an error; absence of a path
// is not (defaults + environment are sufficient to run).
func Load(path string) (*Config, error) {
	k := koanf.New(keyDelim)

	// 1. Built-in defaults (the single source of default values). Loading them
	//    through koanf also gives us the full set of valid keys for the
	//    unknown-env advisory below.
	if err := k.Load(structs.Provider(Default(), "yaml"), nil); err != nil {
		return nil, fmt.Errorf("load defaults: %w", err)
	}
	validKeys := append([]string(nil), k.Keys()...)

	// 2. Optional YAML file (overrides defaults).
	var cfgFileWarning string
	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o044 != 0 {
			cfgFileWarning = fmt.Sprintf("config file %s is readable by group/other (mode %04o); "+
				"it may contain credentials — restrict it to 0600 (or keep secrets in TS2OTEL_* env vars)",
				path, info.Mode().Perm())
		}
	}

	// 3. Environment overrides (highest precedence).
	if err := k.Load(env.Provider(keyDelim, env.Opt{
		Prefix:        EnvPrefix,
		TransformFunc: envTransform,
	}), nil); err != nil {
		return nil, fmt.Errorf("load environment: %w", err)
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
		Tag: "yaml",
		DecoderConfig: &mapstructure.DecoderConfig{
			Result:           &cfg,
			WeaklyTypedInput: true, // env values are strings ("60s", "true", "10")
			DecodeHook:       durationDecodeHook(),
		},
	}); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	cfg.unknownEnv = unknownEnvVars(validKeys)
	cfg.configFileWarning = cfgFileWarning
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
