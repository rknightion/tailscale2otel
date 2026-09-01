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
// and never need to appear in the YAML. Lists of structs remain file-defined,
// with one documented name-keyed overlay for per-tailnet OAuth client secrets.
// The file is optional: with no -config path the process runs from defaults +
// environment alone (handy for containers).
package config

import (
	"fmt"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	env "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"

	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
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

type configBytesProvider []byte

func (p configBytesProvider) ReadBytes() ([]byte, error) { return []byte(p), nil }
func (configBytesProvider) Read() (map[string]any, error) {
	return nil, fmt.Errorf("config byte provider requires a parser")
}

// Config is the root configuration document.
type Config struct {
	LogLevel string `yaml:"log_level" reload:"restart"`
	// LogFormat selects the operational log encoding: "text" (default) or
	// "json". JSON is one record per line, for container and systemd deployments
	// that route logs through a parser (#312). It changes the encoding only —
	// the attributes each call site sets are the same either way.
	LogFormat string          `yaml:"log_format" reload:"restart"`
	Provider  string          `yaml:"provider" reload:"restart"` // "tailscale" (default) | "headscale"
	Tailscale TailscaleConfig `yaml:"tailscale"`
	// Tailnets is the optional multi-tailnet list (MSP mode). When non-empty the
	// instance observes every listed tailnet; it is mutually exclusive with an
	// explicit single tailscale.tailnet (Validate errors if both name a tailnet).
	// Each entry is self-contained (its own name + auth + http). The list itself
	// is file-defined, but a documented name-keyed environment overlay may supply
	// each entry's OAuth client secret without placing that secret in YAML.
	Tailnets          []TailnetConfig         `yaml:"tailnets" reload:"restart"`
	Headscale         HeadscaleConfig         `yaml:"headscale"`
	OTLP              OTLPConfig              `yaml:"otlp"`
	Delivery          DeliveryConfig          `yaml:"delivery"`
	Enrichment        EnrichmentConfig        `yaml:"enrichment"`
	Cardinality       CardinalityConfig       `yaml:"cardinality"`
	Collectors        Collectors              `yaml:"collectors"`
	Scheduler         SchedulerConfig         `yaml:"scheduler"`
	Coordination      CoordinationConfig      `yaml:"coordination"`
	Checkpoint        CheckpointConfig        `yaml:"checkpoint"`
	IngressWAL        IngressWALConfig        `yaml:"ingress_wal"`
	Streaming         StreamingConfig         `yaml:"streaming"`
	Webhook           WebhookConfig           `yaml:"webhook"`
	SelfObservability SelfObservabilityConfig `yaml:"self_observability"`
	PIIFilter         PIIFilterConfig         `yaml:"pii_filter"`
	Admin             AdminConfig             `yaml:"admin"`
	Flows             FlowsConfig             `yaml:"flows"`
	Events            EventsConfig            `yaml:"events"`
	Prometheus        PrometheusConfig        `yaml:"prometheus"`
	Profiling         ProfilingConfig         `yaml:"profiling"`
	Tracing           TracingConfig           `yaml:"tracing"`
	Resource          ResourceConfig          `yaml:"resource"`
	VersionChecks     VersionChecksConfig     `yaml:"version_checks"`
	// GrafanaAnnotations configures the opt-in Grafana annotation writer — the
	// process's only outbound WRITE. Off unless url is set.
	GrafanaAnnotations GrafanaAnnotationsConfig `yaml:"grafana_annotations" reload:"restart"`

	// unknownEnv records TS2OTEL_* environment variables that did not map to any
	// known config key (a likely typo — they were ignored). Unexported, populated
	// by Load, surfaced via Warnings.
	unknownEnv []string

	// configFileWarning is a load-time advisory about the config file itself
	// (currently: loose permissions). Surfaced by Warnings(), like unknownEnv.
	configFileWarning string

	// secretFileConflicts records every "*_file" sibling (see resolveSecretFiles)
	// whose paired value field was ALSO set — a value-XOR-file violation.
	// Populated by Load before Validate runs, so the conflict is reported as a
	// normal Validate error (naming the key pair) rather than a special-cased
	// Load error.
	secretFileConflicts []secretFileConflict

	// pathResolutions records, for every path-bearing field (see pathFields,
	// paths.go), what the operator configured and what this process resolved
	// it to (#310). Populated by Load, before resolveSecretFiles or Validate
	// run, so both can report a "no such file" error naming both paths.
	pathResolutions map[string]pathResolution
}

// CoordinationConfig controls whole-process active-passive coordination. It is
// deliberately Kubernetes-only: mode none preserves the historical singleton
// lifecycle, and kubernetes uses a coordination.k8s.io Lease.
type CoordinationConfig struct {
	Mode          string   `yaml:"mode" reload:"restart"`
	LeaseName     string   `yaml:"lease_name" reload:"restart"`
	Namespace     string   `yaml:"namespace" reload:"restart"`
	LeaseDuration Duration `yaml:"lease_duration" reload:"restart"`
	RenewDeadline Duration `yaml:"renew_deadline" reload:"restart"`
	RetryPeriod   Duration `yaml:"retry_period" reload:"restart"`
}

// DeliveryConfig selects the process-wide telemetry delivery topology. The
// default OTLP mode preserves the historical push-only behavior; prometheus
// suppresses inherited OTLP delivery and serves a pull endpoint, while dual
// enables both paths.
type DeliveryConfig struct {
	Mode string `yaml:"mode" reload:"restart"`
}

// PrometheusPullEnabled reports whether the pull reader and listener are in
// use. prometheus.enabled remains a backwards-compatible opt-in to dual
// delivery when delivery.mode is left at its default of otlp.
func (c *Config) PrometheusPullEnabled() bool {
	return c.Prometheus.Enabled || c.Delivery.Mode == "prometheus" || c.Delivery.Mode == "dual"
}

// PrometheusOnly reports whether inherited OTLP delivery is suppressed. A
// per-signal endpoint can opt one signal back in; Validate requires that
// endpoint when the signal is explicitly enabled in this mode.
func (c *Config) PrometheusOnly() bool { return c.Delivery.Mode == "prometheus" }

// AdminConfig configures the optional always-on admin HTTP server that exposes
// liveness/readiness endpoints (/healthz, /readyz).
type AdminConfig struct {
	Enabled bool   `yaml:"enabled" reload:"restart"`
	Listen  string `yaml:"listen" reload:"restart"`
	// LandingPage (default true) serves a human-readable landing page at "/" and
	// a machine-readable "/api/status.json" on the admin server.
	LandingPage bool `yaml:"landing_page" reload:"restart"`
	// StatusRefreshInterval is how often the landing page's JS re-polls
	// /api/status.json to patch the live view. Default 5s (fleet standard). The
	// page's 1s freshness ticker is independent of this.
	StatusRefreshInterval Duration `yaml:"status_refresh_interval" reload:"restart"`
	// Auth optionally gates the status page and pprof behind a shared secret.
	Auth AdminAuth `yaml:"auth"`
	// TLS optionally serves the admin listener over HTTPS instead of plain HTTP.
	TLS AdminTLS `yaml:"tls"`
	// SupportBundleLogTailRecords bounds the redaction-safe in-memory log tail
	// included in support bundles. Zero disables capture.
	SupportBundleLogTailRecords int `yaml:"support_bundle_log_tail_records" reload:"restart"`
}

// AdminTLS configures TLS for the admin server (mirrors StreamingTLS). Both
// fields are empty by default (plain HTTP); Validate requires both-or-neither
// and that any set path exists and is readable.
type AdminTLS struct {
	CertFile     string `yaml:"cert_file" reload:"file_content"`
	KeyFile      string `yaml:"key_file" reload:"file_content"`
	ClientCAFile string `yaml:"client_ca_file" reload:"file_content"`
	ClientAuth   string `yaml:"client_auth" reload:"restart"`
}

// AdminAuth gates the status page ("/" and "/api/status.json") and the pprof
// handlers behind a shared secret. When Token is set, callers must present it as
// the HTTP Basic password OR as "Authorization: Bearer <token>". The /healthz
// and /readyz probes are never gated. Keep the token in an env var:
// TS2OTEL_ADMIN__AUTH__TOKEN.
type AdminAuth struct {
	Token Secret `yaml:"token" reload:"restart"`
	// TokenFile reads Token from a file at Load (Docker-secrets style). Value XOR
	// file: setting both is a Validate error. The file content is trimmed of
	// surrounding whitespace before use.
	TokenFile string `yaml:"token_file" reload:"file_content"`
	// FailureLimit failures from one source inside FailureWindow trigger
	// FailureBackoff. Zero disables throttling.
	FailureLimit   int      `yaml:"failure_limit" reload:"restart"`
	FailureWindow  Duration `yaml:"failure_window" reload:"restart"`
	FailureBackoff Duration `yaml:"failure_backoff" reload:"restart"`
}

// FlowsConfig configures the built-in flow view served at /flows on the admin
// server. It keeps a bounded, pre-aggregated picture of recent traffic IN MEMORY
// so an operator can explore their tailnet without a metrics backend in the
// loop. Nothing here changes what is exported over OTLP — the backend remains
// the system of record, and the store is lost on restart.
type FlowsConfig struct {
	// Enabled (default true) builds the store and serves /flows. Its only
	// consumer is the admin page, so turning the page off makes the store dead
	// weight; Warnings() says so.
	Enabled bool `yaml:"enabled" reload:"restart"`
	// Retention is how far back the view can see, as a count of one-minute
	// buckets. Memory scales with it (and, in multi-tailnet mode, with the number
	// of tailnets, since each keeps its own store). Bounded to [1m, 24h]: this is
	// an in-memory ring, not a database.
	Retention Duration `yaml:"retention" reload:"restart"`
	// MaxFutureSkew is the largest amount a record may lead the local clock and
	// still enter the in-memory view. It does not affect OTLP emission.
	MaxFutureSkew Duration `yaml:"max_future_skew" reload:"restart"`
	// CapacityProfile trades memory for fidelity on every per-bucket dimension
	// AND the raw-connection ring (#329): "compact" (roughly half the default
	// footprint, folds into "everything else" sooner), "default" (today's
	// hardcoded limits, unchanged), or "expanded" (roughly double). There is
	// no arbitrary/continuous knob — each name is a fixed, hard-coded preset
	// enforced by internal/flowstore.CapsForProfile, so a bad value fails
	// Validate() by name rather than accepting an unbounded raw number.
	CapacityProfile string `yaml:"capacity_profile" reload:"restart"`
	// Store optionally persists /flows to disk (#294) instead of, or in
	// addition to, the in-memory ring above. Off by default (Path == "").
	Store FlowsStoreConfig `yaml:"store"`
}

// FlowsStoreConfig configures the OPT-IN, on-disk SQLite backend for /flows
// (#294): internal/flowstore/sqlitestore. It exists for operators who want
// history beyond flows.retention's in-memory hours, or who want the view to
// survive a restart. Every field maps one-for-one onto
// sqlitestore.Options, and every default below MUST equal that package's
// Default* constant — the two are meant to be read together.
//
// Unlike the in-memory ring, this writes raw connection rows to a file that
// persists across restarts and lands in backups, so Warnings() calls out the
// data-at-rest exposure this introduces once Directory is set.
type FlowsStoreConfig struct {
	// Directory is where the per-tailnet SQLite database files live. Empty
	// (the default) means memory-only: the persistent backend is never built
	// and every field below is inert.
	//
	// Named "directory", not "path", to follow #310's convention: a bare
	// "path" in this config is reserved for HTTP route paths (webhook.path,
	// streaming.path, the node-metrics scrape path), and only the *_file /
	// *_database / file_path / directory tags are treated as filesystem paths
	// and resolved relative to the config file. ingress_wal.directory is the
	// precedent. #237 sketched this key as "flows.store.path"; that name would
	// have collided with the route-path meaning and silently opted out of
	// relative-path resolution, so it was renamed before the first release
	// that ships it.
	Directory string `yaml:"directory" reload:"restart"`
	// Retention is how far back the persistent store keeps rows, independent
	// of flows.retention (which still only sizes the in-memory ring). Rows
	// older than this are swept. Bounded to [1h, 8760h] (365d): unlike the
	// ring, this sizes disk, but an unbounded value would let a forgotten
	// setting grow the database forever.
	Retention Duration `yaml:"retention" reload:"restart"`
	// MaxRows is a hard cap on retained rows, enforced independently of
	// Retention so a traffic flood cannot fill the disk before the next sweep
	// runs. Bounded to [10000, 1000000000].
	MaxRows int64 `yaml:"max_rows" reload:"restart"`
	// MaxExportRows bounds how many rows a single CSV/JSON export may read, so
	// a large retained window cannot be materialized into memory in one
	// request. Bounded to [100, 1000000].
	MaxExportRows int `yaml:"max_export_rows" reload:"restart"`
	// QueueSize bounds the write-behind channel between the hot Record path
	// and the background writer goroutine. A full queue drops the record and
	// counts it rather than blocking the emit path (flowstore's Record
	// contract: never blocks, never fails). Bounded to [64, 1048576].
	QueueSize int `yaml:"queue_size" reload:"restart"`
	// BatchSize is how many queued rows one write transaction commits.
	// Bounded to [1, 100000], and MUST NOT exceed QueueSize — a batch larger
	// than the queue it drains from can never fill.
	BatchSize int `yaml:"batch_size" reload:"restart"`
	// FlushInterval forces a partial batch to disk on a timer, so a quiet
	// tailnet's last few connections do not sit in memory indefinitely
	// waiting for BatchSize to fill. Bounded to [100ms, 5m].
	FlushInterval Duration `yaml:"flush_interval" reload:"restart"`
	// QueryTimeout bounds a single read against the store. A window scan that
	// exceeds it fails honestly rather than hanging the admin page. Bounded to
	// [1s, 5m].
	QueryTimeout Duration `yaml:"query_timeout" reload:"restart"`
	// SweepInterval is how often retention and the row cap are enforced.
	// Bounded to [1m, 24h].
	SweepInterval Duration `yaml:"sweep_interval" reload:"restart"`
	// IncrementalVacuumInterval controls periodic SQLite page reclamation. Zero
	// inherits SweepInterval; IncrementalVacuumPages caps work per tick.
	IncrementalVacuumInterval Duration `yaml:"incremental_vacuum_interval" reload:"restart"`
	IncrementalVacuumPages    int      `yaml:"incremental_vacuum_pages" reload:"restart"`
}

// EventsConfig configures the built-in bounded audit/webhook event explorer
// served at /events on the admin server (#300). It retains a bounded, recent
// window of audit and webhook events IN MEMORY so an operator can filter
// what happened locally — by time, actor, action, target, severity, error
// and type — without a metrics/logs backend in the loop. Nothing here
// changes what is exported over OTLP — the backend remains the system of
// record, and the store is lost on restart.
//
// Unlike FlowsConfig (a ring of one-minute buckets, sized by time), this
// store is a plain ring of individual events sized by COUNT: an audit or
// webhook event is already the meaningful unit, so there is nothing to
// aggregate into a bucket.
type EventsConfig struct {
	// Enabled (default true) builds the store and serves /events. Its only
	// consumer is the admin page, so turning the page off makes the store
	// dead weight; Warnings() says so (mirrors flows.enabled).
	Enabled bool `yaml:"enabled" reload:"restart"`
	// MaxEvents bounds the ring: the most recent MaxEvents audit+webhook
	// events are retained, oldest evicted first. Bounded to [100, 100000] —
	// this sizes process memory, not a database.
	MaxEvents int `yaml:"max_events" reload:"restart"`
}

// PrometheusConfig configures the optional Prometheus pull endpoint (GET /metrics)
// on a DEDICATED listener, independent of the admin server. Off by default. When
// enabled it runs an additional metric.Reader alongside OTLP push, so both export
// paths are active at once (backwards-compat for Prometheus scrapers).
type PrometheusConfig struct {
	Enabled bool           `yaml:"enabled" reload:"restart"`
	Listen  string         `yaml:"listen" reload:"restart"`
	Auth    PrometheusAuth `yaml:"auth"`
	// TLS optionally serves the prometheus listener over HTTPS instead of plain
	// HTTP.
	TLS PrometheusTLS `yaml:"tls"`
	// MaxRequestsInFlight bounds concurrent /metrics gathers. A Gather walks
	// every series in the registry, so N simultaneous slow scrapes cost N times
	// that walk; excess requests get 503 rather than piling up. It must be
	// positive while Prometheus is enabled; zero is rejected rather than
	// inheriting promhttp's unlimited default.
	MaxRequestsInFlight int `yaml:"max_requests_in_flight" reload:"restart"`
	// Timeout caps how long a single /metrics gather may run before the handler
	// gives up with 503. 0 = no timeout (the promhttp default). Keep it below
	// the scraper's own timeout so the app, not the scraper, decides.
	Timeout Duration `yaml:"timeout" reload:"restart"`
	// CoalesceGather serves concurrent scrapes that arrive during one in-flight
	// gather from that single gather's result instead of starting another. Helps
	// when several replicas of a scraper hit the same instance; costs a small
	// amount of staleness.
	CoalesceGather bool `yaml:"coalesce_gather" reload:"restart"`
}

// PrometheusTLS configures TLS for the prometheus pull-endpoint server. Mirrors
// AdminTLS/StreamingTLS; same Validate rules (both-or-neither, files must exist
// and be readable).
type PrometheusTLS struct {
	CertFile string `yaml:"cert_file" reload:"file_content"`
	KeyFile  string `yaml:"key_file" reload:"file_content"`
	// ClientCAFile enables mutual TLS on the prometheus listener: only scrapers
	// presenting a certificate signed by this CA are served. Requires
	// cert_file/key_file (there is no client-cert check without server TLS).
	// Composes with auth.token — a request must satisfy both when both are set.
	ClientCAFile string `yaml:"client_ca_file" reload:"restart"`
	// ClientAuth selects how hard the client certificate is checked:
	// require_and_verify (default when client_ca_file is set), verify_if_given,
	// require, request, or none. Only require_and_verify and verify_if_given
	// actually validate against client_ca_file; the weaker modes exist for
	// staged rollouts and are warned about.
	ClientAuth string `yaml:"client_auth" reload:"restart"`
}

// PrometheusAuth optionally gates /metrics behind a shared secret presented as the
// HTTP Basic password OR "Authorization: Bearer <token>". Empty = open (bind to a
// loopback/tailnet address or rely on network controls). Keep the token in an env
// var: TS2OTEL_PROMETHEUS__AUTH__TOKEN.
type PrometheusAuth struct {
	Token Secret `yaml:"token" reload:"restart"`
	// AllowUnauthenticated acknowledges serving /metrics with NO credential on a
	// network-reachable bind. Without it that combination fails closed with 403,
	// like the admin surface (#315).
	//
	// It exists because, unlike the admin page, remote scraping without a token is
	// a legitimate deployment: an in-cluster Prometheus reaching a pod behind a
	// NetworkPolicy has network-level control that this process cannot see. A flat
	// refusal would break that; a silent default-open is how every series —
	// device names, flow endpoints, audit identities — ends up on an accidentally
	// published port. So the operator says so explicitly.
	//
	// It only covers the NO-TOKEN case. A configured token is always enforced.
	AllowUnauthenticated bool `yaml:"allow_unauthenticated" reload:"restart"`
	// TokenFile reads Token from a file at Load (Docker-secrets style). Value XOR
	// file: setting both is a Validate error. The file content is trimmed of
	// surrounding whitespace before use.
	TokenFile string `yaml:"token_file" reload:"file_content"`
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
	MutexProfileFraction int `yaml:"mutex_profile_fraction" reload:"restart"`
	BlockProfileRate     int `yaml:"block_profile_rate" reload:"restart"`
}

// ProfilingPprof toggles the net/http/pprof debug handlers, which are mounted on
// the admin HTTP server (so it requires admin.enabled).
type ProfilingPprof struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
}

// ProfilingPyroscope configures the Pyroscope continuous-profiling push agent.
// When enabled it requires ServerAddress; the basic-auth/tenant fields cover
// Grafana Cloud Profiles and multi-tenant servers.
type ProfilingPyroscope struct {
	Enabled           bool   `yaml:"enabled" reload:"restart"`
	ServerAddress     string `yaml:"server_address" reload:"restart"`
	BasicAuthUser     string `yaml:"basic_auth_user" reload:"restart"`
	BasicAuthPassword Secret `yaml:"basic_auth_password" reload:"restart"`
	// BasicAuthPasswordFile reads BasicAuthPassword from a file at Load
	// (Docker-secrets style). Value XOR file: setting both is a Validate error.
	// The file content is trimmed of surrounding whitespace before use.
	BasicAuthPasswordFile string            `yaml:"basic_auth_password_file" reload:"restart"`
	TenantID              string            `yaml:"tenant_id" reload:"restart"`
	UploadRate            Duration          `yaml:"upload_rate" reload:"restart"`
	Tags                  map[string]string `yaml:"tags" reload:"restart"`
	// Headers are sent on every profile upload. Values are Secret: a profiles
	// endpoint behind a gateway commonly wants an API key here. Reserved
	// headers (Authorization when basic auth is set, and the tenant header)
	// win over anything set here rather than being silently overridden.
	Headers map[string]Secret `yaml:"headers" reload:"restart"`
	// TLS configures the profile-upload client's TLS when server_address is
	// https. Distinct from the listener TLS blocks: this is an outbound client,
	// so it takes a CA to trust plus an optional client keypair for mTLS.
	TLS PyroscopeTLS `yaml:"tls"`

	// SpanProfiles correlates sampled CPU profiles with trace spans (#370).
	SpanProfiles SpanProfilesConfig `yaml:"span_profiles"`

	// TailnetLabel controls whether continuous profiles carry a tailnet
	// dimension, and in what form (#376): off (the default), hashed (a stable
	// 12-hex-char SHA-256 prefix), or name (the literal tailnet name).
	//
	// It is off by default and deliberately NOT covered by the pii_filter
	// categories, because those govern the metric/log/span pipeline and profiles
	// are a different destination with a different audience. A tailnet name is a
	// customer identifier; hashed gives an MSP the "which tenant is burning the
	// CPU" answer without shipping it, and name is available for a single-tenant
	// operator profiling their own tailnet.
	TailnetLabel string `yaml:"tailnet_label" reload:"restart"` // off|hashed|name

	// CredentialReload rotates the password/header/TLS files this agent reads
	// without restarting the process (#362).
	CredentialReload CredentialReloadConfig `yaml:"credential_reload"`
}

// PyroscopeTLS configures TLS for the OUTBOUND Pyroscope profile-upload client.
// Mirrors the field names of otlp.tls minus `insecure`: there is no plaintext
// toggle because the scheme in server_address already decides http vs https.
type PyroscopeTLS struct {
	// InsecureSkipVerify keeps TLS on but skips server-certificate
	// verification. A footgun — prefer ca_file with the gateway's CA.
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify" reload:"restart"`
	CAFile             string `yaml:"ca_file" reload:"file_content"`
	CertFile           string `yaml:"cert_file" reload:"file_content"`
	KeyFile            string `yaml:"key_file" reload:"file_content"`
}

// VersionChecksConfig configures the optional outbound "is a newer release
// available?" checks. Both sub-checks make external HTTPS calls and are
// fail-open (a failed/blocked fetch silently emits nothing). cache_ttl bounds
// how often the upstream endpoints are hit.
type VersionChecksConfig struct {
	Self     VersionCheckSelf    `yaml:"self"`
	Devices  VersionCheckDevices `yaml:"devices"`
	CacheTTL Duration            `yaml:"cache_ttl" reload:"restart"`
	Timeout  Duration            `yaml:"timeout" reload:"restart"`
}

// VersionCheckSelf gates the self update-available gauge (tailscale2otel.update_available),
// comparing the running build to the latest tailscale2otel GitHub release.
type VersionCheckSelf struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
}

// VersionCheckDevices gates the per-device/fleet Tailscale-client version-skew
// metrics, comparing each device's client version to the latest Tailscale stable.
type VersionCheckDevices struct {
	Enabled                bool `yaml:"enabled" reload:"restart"`
	OutdatedMinorThreshold int  `yaml:"outdated_minor_threshold" reload:"restart"`
}

// HeadscaleConfig holds Headscale control-plane connection settings (used when
// provider: headscale). Auth is a Bearer API key; keep it in env (TS2OTEL_*).
type HeadscaleConfig struct {
	URL    string `yaml:"url" reload:"restart"`
	APIKey Secret `yaml:"api_key" reload:"restart"`
	// APIKeyFile reads APIKey from a file at Load (Docker-secrets style). Value
	// XOR file: setting both is a Validate error. The file content is trimmed of
	// surrounding whitespace before use.
	APIKeyFile string              `yaml:"api_key_file" reload:"restart"`
	HTTP       TailscaleHTTPConfig `yaml:"http"` // reuse the same timeout/retry/rate_limit shape
	// IPPrefixes lists the private/CGNAT/ULA address ranges allocated by this
	// Headscale deployment. Empty preserves the Tailscale defaults. Validation
	// rejects public and overly broad prefixes because this set widens the
	// node-metrics scraper's SSRF allowlist as well as address classification.
	IPPrefixes []string `yaml:"ip_prefixes" reload:"restart"`
	// MaxResponseBytes bounds a single successful JSON response body before it
	// is decoded, capping the exporter's peak decode memory (#488 — the Headscale
	// client had the same post-hoc cap #474 removed from the Tailscale one).
	// Fleet-wide, like tailscale.max_response_bytes: a process-memory safety
	// budget, not per-endpoint connection policy. Headscale exposes only snapshot
	// resources (no bulk log pull), so there is a single budget rather than the
	// snapshot/log pair. See internal/hsapi/limit.go for the sizing evidence and
	// the per-deployment tuning constraint.
	MaxResponseBytes int64 `yaml:"max_response_bytes" reload:"restart"`
}

// TailscaleConfig holds Tailscale API connection settings.
type TailscaleConfig struct {
	Tailnet string              `yaml:"tailnet" reload:"restart"`
	Auth    TailscaleAuth       `yaml:"auth"`
	HTTP    TailscaleHTTPConfig `yaml:"http"`
	// MaxResponseBytes bounds a single successful JSON response body from a
	// snapshot endpoint (devices, keys, dns, services, …) before it is decoded.
	// MaxLogResponseBytes is the same ceiling for the bulk log pulls (flow logs,
	// audit logs), which are legitimately multi-MB. They bound the exporter's
	// peak decode memory (#474); see internal/tsapi/limit.go for the sizing
	// evidence and the per-tailnet tuning constraint.
	//
	// Fleet-wide on purpose: these are process-memory safety budgets, not
	// per-tailnet connection policy, so a tailnets[] entry does NOT override them
	// — the top-level values apply to every tailnet runtime (and are therefore
	// reachable from the environment in multi-tailnet mode, which a file-only
	// tailnets[] field would not be).
	MaxResponseBytes    int64 `yaml:"max_response_bytes" reload:"restart"`
	MaxLogResponseBytes int64 `yaml:"max_log_response_bytes" reload:"restart"`
	// Organization enables alpha organization-tailnet roster discovery when
	// non-empty. Empty preserves explicit single- or multi-tailnet config.
	Organization string `yaml:"organization" reload:"restart"`
}

// TailnetConfig is one entry in the multi-tailnet list. It mirrors the
// connection-bearing fields of TailscaleConfig but names the tailnet explicitly.
type TailnetConfig struct {
	Name        string              `yaml:"name" reload:"restart"`
	Auth        TailscaleAuth       `yaml:"auth"`
	HTTP        TailscaleHTTPConfig `yaml:"http"`
	Cardinality TailnetCardinality  `yaml:"cardinality"`
	// ObjectStore holds THIS tailnet's object-store ingestion destinations, one
	// per signal. In multi-tailnet mode it is the only place a destination may
	// come from: nothing is inherited from collectors.flowlogs.objectstore, and
	// two entries may not name the same feed (#284). Like the rest of the list it
	// is file-only, so a static credential must arrive through its *_file sibling
	// (a mounted Secret) or the ambient chain — see FlowObjectStore.
	ObjectStore TailnetObjectStore `yaml:"objectstore"`
}

// TailnetCardinality provides file-only per-tailnet overrides. Zero inherits
// the corresponding global cardinality value, a negative metric_limit means
// unlimited for this tailnet only, and a positive value is the explicit limit.
type TailnetCardinality struct {
	MetricLimit       int `yaml:"metric_limit" reload:"restart"`
	WarningThreshold  int `yaml:"warning_threshold" reload:"restart"`
	CriticalThreshold int `yaml:"critical_threshold" reload:"restart"`
}

// TailnetObjectStore groups one tailnet's per-signal object-store destinations.
//
// The two signals are separate destinations with separate checkpoint namespaces
// and nothing is shared between them: Tailscale publishes network (flow) and
// configuration (audit) logs as independent exports, so one runtime can read one,
// the other, or both. Two destinations reachable by this process may not name the
// same feed — see validateObjectStoreFeeds.
type TailnetObjectStore struct {
	Flow  ObjectStoreConfig `yaml:"flow"`
	Audit ObjectStoreConfig `yaml:"audit"`
	// K8sAudit is the tsrecorder Kubernetes API-audit destination. It is a
	// separate bucket with its own key layout from Flow and Audit, and is
	// never inherited from collectors.k8s_audit.objectstore in multi-tailnet
	// mode, exactly like the other two signals.
	K8sAudit ObjectStoreConfig `yaml:"k8s_audit"`
}

// ResolvedTailnet is the normalized, per-tailnet connection config the app layer
// iterates. Both single mode (one tailscale: block) and multi mode (a tailnets:
// list) collapse to a []ResolvedTailnet via ResolvedTailnets.
type ResolvedTailnet struct {
	Name string
	Auth TailscaleAuth
	HTTP TailscaleHTTPConfig
	// Cardinality* are the effective per-tailnet values after applying the
	// file-only tailnets[].cardinality overrides. MetricLimit preserves a
	// negative value as the explicit unlimited setting for this tailnet.
	CardinalityMetricLimit       int
	CardinalityWarningThreshold  int
	CardinalityCriticalThreshold int
	// MaxResponseBytes / MaxLogResponseBytes are the fleet-wide decode budgets
	// from the top-level tailscale: block, copied onto every resolved tailnet so
	// the app layer has one place to read them from (#474).
	MaxResponseBytes    int64
	MaxLogResponseBytes int64
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
	maxBytes, maxLogBytes := c.decodeBudgets()
	if len(c.Tailnets) > 0 {
		// Effective fleet base: the top-level tailscale.http block with any zero
		// field filled from the built-in defaults, so backfill works even when the
		// top-level block is entirely unset in multi mode.
		base := mergeHTTPDefaults(c.Tailscale.HTTP, Default().Tailscale.HTTP)
		out := make([]ResolvedTailnet, len(c.Tailnets))
		for i, t := range c.Tailnets {
			auth := t.Auth
			// Least-privilege default (#127): an OAuth entry that omits scopes would
			// otherwise request an UNSCOPED token (every scope the client holds).
			// Match the single-tailnet Default() and pin it to all:read.
			if auth.Method == "oauth" && len(auth.OAuth.Scopes) == 0 {
				auth.OAuth.Scopes = []string{"all:read"}
			}
			metricLimit, warning, critical := c.effectiveTailnetCardinality(t.Cardinality)
			out[i] = ResolvedTailnet{
				Name:                         t.Name,
				Auth:                         auth,
				HTTP:                         mergeHTTPDefaults(t.HTTP, base),
				CardinalityMetricLimit:       metricLimit,
				CardinalityWarningThreshold:  warning,
				CardinalityCriticalThreshold: critical,
				MaxResponseBytes:             maxBytes,
				MaxLogResponseBytes:          maxLogBytes,
			}
		}
		return out
	}
	return []ResolvedTailnet{{
		Name:                         c.Tailscale.Tailnet,
		Auth:                         c.Tailscale.Auth,
		HTTP:                         c.Tailscale.HTTP,
		CardinalityMetricLimit:       c.Cardinality.MetricLimit,
		CardinalityWarningThreshold:  c.Cardinality.WarningThreshold,
		CardinalityCriticalThreshold: c.Cardinality.CriticalThreshold,
		MaxResponseBytes:             maxBytes,
		MaxLogResponseBytes:          maxLogBytes,
	}}
}

// effectiveTailnetCardinality resolves the file-only override block onto the
// global cardinality settings. A zero value inherits independently; unlike the
// global limit, a negative per-tailnet metric limit is retained as an explicit
// unlimited value for that tailnet.
func (c *Config) effectiveTailnetCardinality(override TailnetCardinality) (metricLimit, warning, critical int) {
	metricLimit = override.MetricLimit
	if metricLimit == 0 {
		metricLimit = c.Cardinality.MetricLimit
	}
	warning = override.WarningThreshold
	if warning == 0 {
		warning = c.Cardinality.WarningThreshold
	}
	critical = override.CriticalThreshold
	if critical == 0 {
		critical = c.Cardinality.CriticalThreshold
	}
	return metricLimit, warning, critical
}

// decodeBudgets returns the effective fleet-wide response decode budgets,
// falling back to the built-in defaults when a value is unset or non-positive
// (a zero would otherwise reach tsapi as "no budget configured"; tsapi defaults
// it too, but resolving here keeps the app layer honest about what is in force).
func (c *Config) decodeBudgets() (maxBytes, maxLogBytes int64) {
	d := Default().Tailscale
	maxBytes, maxLogBytes = c.Tailscale.MaxResponseBytes, c.Tailscale.MaxLogResponseBytes
	if maxBytes <= 0 {
		maxBytes = d.MaxResponseBytes
	}
	if maxLogBytes <= 0 {
		maxLogBytes = d.MaxLogResponseBytes
	}
	return maxBytes, maxLogBytes
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
	Method           string                 `yaml:"method" reload:"restart"`
	OAuth            OAuthConfig            `yaml:"oauth"`
	APIKey           Secret                 `yaml:"apikey" reload:"restart"`
	WorkloadIdentity WorkloadIdentityConfig `yaml:"workload_identity"`
	// APIKeyFile reads APIKey from a file at Load (Docker-secrets style). Value
	// XOR file: setting both is a Validate error. The file content is trimmed of
	// surrounding whitespace before use. TailnetConfig entries embed this struct,
	// so tailnets[].auth.apikey_file gets the same behavior for free.
	APIKeyFile string `yaml:"apikey_file" reload:"restart"`
}

// WorkloadIdentityConfig holds workload-identity-federation settings
// (auth.method: workload_identity): a federated OAuth client ID plus the path
// to an OIDC ID token (e.g. a Kubernetes projected service-account token)
// exchanged for a short-lived Tailscale API access token via
// POST /api/v2/oauth/token-exchange. The token file is re-read on every
// exchange (projected tokens rotate in place). There is no scopes field:
// scopes are fixed by the federated identity's admin-console configuration,
// not requested in the exchange.
type WorkloadIdentityConfig struct {
	ClientID    string `yaml:"client_id" reload:"restart"`
	IDTokenFile string `yaml:"id_token_file" reload:"restart"`
}

// OAuthConfig holds OAuth client-credentials settings.
type OAuthConfig struct {
	ClientID     string `yaml:"client_id" reload:"restart"`
	ClientSecret Secret `yaml:"client_secret" reload:"restart"`
	// ClientSecretFile reads ClientSecret from a file at Load (Docker-secrets
	// style). Value XOR file: setting both is a Validate error. The file content
	// is trimmed of surrounding whitespace before use. TailnetConfig entries embed
	// TailscaleAuth (and so this struct), so tailnets[].auth.oauth.client_secret_file
	// gets the same behavior for free.
	ClientSecretFile string   `yaml:"client_secret_file" reload:"restart"`
	Scopes           []string `yaml:"scopes" reload:"restart"`
}

// TailscaleHTTPConfig configures the HTTP client used for the Tailscale API.
type TailscaleHTTPConfig struct {
	Timeout Duration    `yaml:"timeout" reload:"restart"`
	Retry   RetryConfig `yaml:"retry"`
	// RateLimit caps the global request rate (requests/second) across every
	// collector. Zero (the default) means unlimited.
	RateLimit float64 `yaml:"rate_limit" reload:"restart"`
}

// RetryConfig configures exponential backoff retries.
type RetryConfig struct {
	MaxAttempts int      `yaml:"max_attempts" reload:"restart"`
	BaseDelay   Duration `yaml:"base_delay" reload:"restart"`
	MaxDelay    Duration `yaml:"max_delay" reload:"restart"`
}

// OTLPConfig configures the OTLP exporter.
type OTLPConfig struct {
	Protocol     string             `yaml:"protocol" reload:"restart"`
	Endpoint     string             `yaml:"endpoint" reload:"restart"`
	GrafanaCloud GrafanaCloudConfig `yaml:"grafana_cloud"`
	// Headers values are Secret: an OTLP header commonly carries an
	// Authorization token (the documented way to auth against a non-Grafana-Cloud
	// gateway), so the values redact themselves in any config dump/log (#73).
	Headers map[string]Secret `yaml:"headers" reload:"restart"`
	// Limits bound one individual log record before export. Distinct from the
	// receivers' request-body caps, which bound a whole inbound request: a
	// request can be perfectly valid and still contain one enormous record.
	Limits         OTLPLimits `yaml:"limits"`
	TLS            TLSConfig  `yaml:"tls"`
	MetricInterval Duration   `yaml:"metric_interval" reload:"restart"`
	// MetricExportBatchSize bounds the number of datapoints in each OTLP metric
	// request. This is a count, not a serialized-byte limit.
	MetricExportBatchSize int `yaml:"metric_export_batch_size" reload:"restart"`
	// MetricTemporality selects cumulative (Grafana Cloud default) or delta.
	MetricTemporality string `yaml:"metric_temporality" reload:"restart"`
	// OutageSummaryInterval controls re-summary of a continuing export outage.
	OutageSummaryInterval Duration `yaml:"outage_summary_interval" reload:"restart"`

	// CredentialReload rotates the token/header/TLS files the OTLP exporters use
	// without restarting the process (#362).
	CredentialReload CredentialReloadConfig `yaml:"credential_reload"`

	// Compression is the OTLP request compression: gzip or none. Empty defers to
	// the standard OTEL_EXPORTER_OTLP[_<SIGNAL>]_COMPRESSION variables, then the
	// exporter default (#360).
	Compression string `yaml:"compression" reload:"restart"`
	// Timeout bounds one export request. Zero defers to
	// OTEL_EXPORTER_OTLP[_<SIGNAL>]_TIMEOUT, then the exporter's 10s default.
	Timeout Duration `yaml:"timeout" reload:"restart"`
	// MaxRequestSize caps one serialized request in bytes. This is a client-side
	// rejection GUARD, not a splitter: it fails an oversized request fast instead
	// of shipping it into a backend 413. The knob that actually keeps requests
	// under an ingest limit is metric_export_batch_size above.
	MaxRequestSize int `yaml:"max_request_size" reload:"restart"`
	// GRPCReconnectionPeriod forces a fresh gRPC connection attempt after this
	// long. gRPC only; ignored for http and stdout.
	GRPCReconnectionPeriod Duration `yaml:"grpc_reconnection_period" reload:"restart"`
	// Retry is the exporter's retry policy. Zero leaves the exporter default.
	Retry OTLPRetryConfig `yaml:"retry"`

	// Batch tunes the log and span processor queues (#358). The SDK's queues are
	// bounded and drop silently under a receiver burst or a stalled backend, so
	// these exist alongside the saturation/drop telemetry.
	Batch OTLPBatchConfig `yaml:"batch"`

	// Stdout makes the stdout protocol an immediate debugging sink (#384).
	Stdout OTLPStdoutConfig `yaml:"stdout"`

	// Metrics, Logs and Traces optionally send one signal somewhere else — a
	// different collector, tenant, credential or protocol (#361). An unset field
	// inherits the common block above; credentials are never shared across a
	// signal boundary, so a signal that sets its own headers does not also
	// inherit these.
	Metrics OTLPSignalConfig `yaml:"metrics"`
	Logs    OTLPSignalConfig `yaml:"logs"`
	Traces  OTLPSignalConfig `yaml:"traces"`
}

// OTLPRetryConfig is the exporter retry policy (#360). Enabled is a *bool so
// "unset" is distinguishable from an explicit false: unset leaves the exporter's
// own default (retry on), while false genuinely disables it.
type OTLPRetryConfig struct {
	Enabled         *bool    `yaml:"enabled" reload:"restart"`
	InitialInterval Duration `yaml:"initial_interval" reload:"restart"`
	MaxInterval     Duration `yaml:"max_interval" reload:"restart"`
	MaxElapsedTime  Duration `yaml:"max_elapsed_time" reload:"restart"`
}

// OTLPQueueConfig tunes one signal's processor queue (#358). Zero values mean
// "leave the SDK default", so an untouched block reproduces today's behavior.
type OTLPQueueConfig struct {
	MaxQueueSize       int      `yaml:"max_queue_size" reload:"restart"`
	ExportMaxBatchSize int      `yaml:"export_max_batch_size" reload:"restart"`
	ExportInterval     Duration `yaml:"export_interval" reload:"restart"`
	ExportTimeout      Duration `yaml:"export_timeout" reload:"restart"`
}

// OTLPBatchConfig holds the per-signal queue settings. Metrics are absent by
// design: a PeriodicReader has no queue to saturate, so there is nothing to tune
// or drop — its cadence is otlp.metric_interval.
type OTLPBatchConfig struct {
	Logs   OTLPQueueConfig `yaml:"logs"`
	Traces OTLPQueueConfig `yaml:"traces"`
}

// OTLPStdoutConfig makes the stdout protocol immediate (#384). It applies only
// when otlp.protocol is stdout; stdout is a debugging sink and carries no
// reliability or rotation promise.
type OTLPStdoutConfig struct {
	// MetricInterval replaces the 60s production cadence so a debugging run does
	// not wait a minute to see a metric. Zero uses the built-in stdout default.
	MetricInterval Duration `yaml:"metric_interval" reload:"restart"`
	// Pretty indents the emitted JSON.
	Pretty bool `yaml:"pretty" reload:"restart"`
}

// OTLPSignalConfig overrides the destination for one signal (#361). Every field
// is "unset means inherit"; the pointer fields exist so an explicit false is
// distinguishable from an omitted key.
type OTLPSignalConfig struct {
	Enabled  *bool             `yaml:"enabled" reload:"restart"`
	Protocol string            `yaml:"protocol" reload:"restart"`
	Endpoint string            `yaml:"endpoint" reload:"restart"`
	Headers  map[string]Secret `yaml:"headers" reload:"restart"`
	TLS      OTLPSignalTLS     `yaml:"tls"`

	Compression            string          `yaml:"compression" reload:"restart"`
	Timeout                Duration        `yaml:"timeout" reload:"restart"`
	MaxRequestSize         int             `yaml:"max_request_size" reload:"restart"`
	GRPCReconnectionPeriod Duration        `yaml:"grpc_reconnection_period" reload:"restart"`
	Retry                  OTLPRetryConfig `yaml:"retry"`
}

// OTLPSignalTLS is a per-signal TLS override. The two insecure flags are *bool
// so a signal can turn plaintext OFF while the common block has it on.
type OTLPSignalTLS struct {
	Insecure           *bool  `yaml:"insecure" reload:"restart"`
	InsecureSkipVerify *bool  `yaml:"insecure_skip_verify" reload:"restart"`
	CAFile             string `yaml:"ca_file" reload:"restart"`
	CertFile           string `yaml:"cert_file" reload:"restart"`
	KeyFile            string `yaml:"key_file" reload:"restart"`
}

// GrafanaCloudConfig holds Grafana Cloud OTLP credentials.
type GrafanaCloudConfig struct {
	InstanceID string `yaml:"instance_id" reload:"restart"`
	Token      Secret `yaml:"token" reload:"restart"`
	// TokenFile reads Token from a file at Load (Docker-secrets style). Value XOR
	// file: setting both is a Validate error. The file content is trimmed of
	// surrounding whitespace before use.
	TokenFile string `yaml:"token_file" reload:"file_content"`
}

// TLSConfig configures transport security for OTLP.
type TLSConfig struct {
	// Insecure disables TLS entirely (plaintext h2c / http://) — NOT a
	// certificate-verification skip. Use InsecureSkipVerify for that.
	Insecure bool `yaml:"insecure" reload:"restart"`
	// InsecureSkipVerify keeps TLS on but skips server-certificate verification
	// (self-signed / private-CA OTLP gateways, for testing). Default false. A
	// footgun — prefer ca_file with the gateway's CA in production.
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify" reload:"restart"`
	CAFile             string `yaml:"ca_file" reload:"file_content"`
	CertFile           string `yaml:"cert_file" reload:"file_content"`
	KeyFile            string `yaml:"key_file" reload:"file_content"`
}

// OTLPLimits bounds an individual OTLP log record (#366). One oversized HEC,
// audit or webhook record could otherwise dominate a batch or breach a backend's
// per-record limit even though the request carrying it was valid.
//
// Truncation is UTF-8 safe (a multi-byte rune is never split), happens AFTER
// redaction so a secret can never be truncated into a partially-redacted string,
// and leaves an explicit marker. It applies only to log bodies and log attribute
// VALUES — never to metric labels, which must stay byte-exact or series split.
//
// There is deliberately no "unlimited" setting: bounding the record is the point.
// An operator who wants effectively no bound sets a very large value, which is an
// explicit choice rather than a zero that silently means "no protection".
type OTLPLimits struct {
	// LogBodyBytes caps the log record body.
	LogBodyBytes int `yaml:"log_body_bytes" reload:"restart"`
	// LogAttributeValueBytes caps each individual string-valued log attribute.
	// Non-string attribute kinds are fixed-size by construction and unaffected.
	LogAttributeValueBytes int `yaml:"log_attribute_value_bytes" reload:"restart"`
}

// EnrichmentConfig configures device-enrichment caching.
type EnrichmentConfig struct {
	CacheTTL              Duration         `yaml:"cache_ttl" reload:"restart"`
	DeviceCacheStaleAfter Duration         `yaml:"device_cache_stale_after" reload:"restart"`
	ReverseDNS            ReverseDNSConfig `yaml:"reverse_dns"`
	GeoIP                 GeoIPConfig      `yaml:"geoip"`
}

// GeoIPConfig configures opt-in geolocation and autonomous-system enrichment of
// EXTERNAL (non-Tailscale) addresses, from MaxMind DB files on local disk.
//
// Lookups are purely local — the databases are loaded into memory and a lookup
// never touches the network. The optional Download block keeps those files
// current from MaxMind's download API; an operator who already runs geoipupdate
// (or mounts the files in) leaves it off and just points the two paths at them,
// and ReloadInterval picks up whatever rewrites them.
type GeoIPConfig struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
	// CountryDatabase is a GeoLite2/GeoIP2 Country .mmdb. A City database is
	// accepted (it is a superset) but only the country and continent fields are
	// ever read — locality and lat/lon are a genuine cardinality problem and are
	// deliberately not emitted.
	CountryDatabase string `yaml:"country_database" reload:"file_content"`
	// ASNDatabase is a GeoLite2/GeoIP2 ASN .mmdb.
	ASNDatabase string `yaml:"asn_database" reload:"file_content"`
	// ReloadInterval is how often the configured paths are re-stat'ed and a
	// changed file hot-swapped in. This is what makes the operator-managed path
	// work, and it is NOT the same clock as download.interval — that one asks
	// MaxMind for a newer build, this one notices a file that changed on disk.
	// 0 disables reloading (the databases are then loaded once at startup).
	ReloadInterval Duration `yaml:"reload_interval" reload:"restart"`
	// Download keeps the database files current from MaxMind directly.
	Download GeoIPDownloadConfig `yaml:"download"`
	// AcknowledgeCardinality silences the startup advisory that fires when
	// cardinality.flow.geo_dims puts country labels on the RAW flow-metric
	// families. Purely an acknowledgement; it changes no emission.
	AcknowledgeCardinality bool `yaml:"acknowledge_cardinality" reload:"restart"`
}

// GeoIPDownloadConfig configures the built-in MaxMind downloader. It is a
// convenience so a container needs no sidecar; leaving it off and supplying the
// files by any other means is equally supported.
type GeoIPDownloadConfig struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
	// AccountID and LicenseKey are the MaxMind credentials, sent as HTTP Basic
	// auth. A free GeoLite2 account is enough.
	AccountID  string `yaml:"account_id" reload:"restart"`
	LicenseKey Secret `yaml:"license_key" reload:"restart"`
	// LicenseKeyFile is the *_file sibling of LicenseKey (value XOR file).
	LicenseKeyFile string `yaml:"license_key_file" reload:"restart"`
	// Editions are the MaxMind edition IDs to fetch, e.g. GeoLite2-Country and
	// GeoLite2-ASN. Each is installed as <directory>/<edition>.mmdb.
	Editions []string `yaml:"editions" reload:"restart"`
	// Directory is where downloaded databases are installed. Empty selects
	// <state dir>/geoip, the same platform-appropriate location the checkpoint
	// file uses.
	Directory string `yaml:"directory" reload:"restart"`
	// Interval is how often to ask MaxMind for a newer build. Each check is a
	// conditional request, so an unchanged database costs a 304 and does not
	// count against MaxMind's daily download limit.
	Interval Duration `yaml:"interval" reload:"restart"`
	// Timeout bounds one edition's download end to end.
	Timeout Duration `yaml:"timeout" reload:"restart"`
	// Endpoint is the download API base; empty selects MaxMind's.
	Endpoint string `yaml:"endpoint" reload:"restart"`
}

// ReverseDNSConfig configures opt-in reverse-DNS (PTR) enrichment of EXTERNAL
// (non-Tailscale) flow addresses. When enabled, a resolved hostname replaces the
// "external" bucket / raw IP in tailscale.src.node / tailscale.dst.node on flow
// logs and metrics. Lookups are async and cached; the hot path never blocks.
type ReverseDNSConfig struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
	// Server is the resolver to query as "ip" or "ip:port" (default port 53). Empty
	// uses the system/container default resolver.
	Server      string   `yaml:"server" reload:"restart"`
	Timeout     Duration `yaml:"timeout" reload:"restart"`      // per-lookup timeout
	CacheTTL    Duration `yaml:"cache_ttl" reload:"restart"`    // positive-result TTL
	NegativeTTL Duration `yaml:"negative_ttl" reload:"restart"` // failed-lookup TTL
	// StaleTTL is how long past cache_ttl a resolved name may still be served
	// while one background refresh runs (#297). Without it, the instant an
	// entry expires the flow label falls back to "external" until the refresh
	// lands, so tailscale.src/dst.node flaps hostname -> external -> hostname
	// on every TTL boundary and splits the metric series. 0 disables stale
	// serving and restores the immediate-miss behavior. Negative (failed)
	// lookups are never served stale.
	StaleTTL   Duration `yaml:"stale_ttl" reload:"restart"`
	MaxEntries int      `yaml:"max_entries" reload:"restart"` // cache size bound
	// AcknowledgeCardinality silences the startup advisory that fires when
	// reverse_dns.enabled=true AND cardinality.flow.node_dims=true (the only
	// combination where PTR names inflate flow-METRIC cardinality). Set to true
	// once you have sized cardinality.metric_limit for the added per-external-IP
	// series — it is purely an acknowledgement and does not change emission.
	AcknowledgeCardinality bool `yaml:"acknowledge_cardinality" reload:"restart"`
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
	MetricLimit int `yaml:"metric_limit" reload:"restart"`
	// DerpRegionRollup (default true) gates the tailnet-wide per-DERP-region
	// rollup gauges (tailscale.derp.region.*) emitted by the devices collector.
	DerpRegionRollup bool `yaml:"derp_region_rollup" reload:"restart"`
	// SubnetRouteRollup (default true) gates the per-CIDR
	// tailscale.subnet_routes.routers redundancy gauge (one series per subnet
	// CIDR). The fleet exit/subnet count aggregates are emitted regardless.
	SubnetRouteRollup bool `yaml:"subnet_route_rollup" reload:"restart"`
	// Flow shapes the flow-metric families and their attributes.
	Flow FlowCardinality `yaml:"flow"`
	// PerEntity gates the per-entity gauges of the inventory collectors.
	PerEntity PerEntityCardinality `yaml:"per_entity"`
	// WarningThreshold and CriticalThreshold flag a source metric on the admin
	// status page's cardinality view when its active-series count crosses them
	// (self-observability only; 0 disables that level). When both are set,
	// CriticalThreshold must be >= WarningThreshold, and when MetricLimit>0 both
	// must be <= MetricLimit. Defaults 2000 / 8000.
	WarningThreshold  int `yaml:"warning_threshold" reload:"restart"`
	CriticalThreshold int `yaml:"critical_threshold" reload:"restart"`
	// LabelValueSampleCap bounds how many distinct values per (metric, label) the
	// self-observability cardinality tracker retains to power the label-cardinality
	// views on the status page. Beyond the cap the label is marked capped and its
	// example values truncated (a memory guard for genuinely high-cardinality
	// labels such as per-flow IPs). 0 disables label-value capture. Default 100.
	LabelValueSampleCap int `yaml:"label_value_sample_cap" reload:"restart"`
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
	MetricsMode string `yaml:"metrics_mode" reload:"restart"`
	// RollupTopN bounds the number of busiest source/destination node pairs the
	// flow-metrics rollup keeps per flush (only used when MetricsMode is rollup or
	// both). Pairs beyond it fold into the __other__ series. 0 selects the default
	// (500).
	RollupTopN int `yaml:"rollup_top_n" reload:"restart"`
	// SourcePort / DestinationPort independently add source.port / destination.port
	// to flow METRICS (both default false; flow LOGS always carry both ports).
	SourcePort      bool `yaml:"source_port" reload:"restart"`
	DestinationPort bool `yaml:"destination_port" reload:"restart"`
	// NodeDims (default true) includes the src/dst device names on flow metrics.
	NodeDims bool `yaml:"node_dims" reload:"restart"`
	// IdentityDims adds the per-flow endpoint identity — tailscale.{src,dst}.user,
	// .tags and .os, taken from the srcNode/dstNodes blocks the control plane
	// embeds in each flow record — to flow METRICS. Default false; flow LOGS
	// always carry it. The values are low-cardinality (bounded by user, tag and OS
	// counts, all far below device count) but user is an email address, so it
	// stays off the default metric surface. PII filtering still applies.
	IdentityDims bool `yaml:"identity_dims" reload:"restart"`
	// CollapseExternal (default true) buckets unresolved IPs as external/unknown.
	CollapseExternal bool `yaml:"collapse_external" reload:"restart"`
	// ExitNodeAttribution (default true) emits the bounded
	// tailscale.exit_node.io/packets counters attributing exit traffic to the
	// relaying node. Bounded by exit-node count; independent of MetricsMode.
	ExitNodeAttribution bool `yaml:"exit_node_attribution" reload:"restart"`
	// GeoDims adds source/destination geo.country.iso_code and
	// geo.continent.code to flow METRICS when enrichment.geoip is on. Default
	// false; flow LOGS always carry them (along with the ASN, which never
	// reaches a metric at all).
	//
	// The cost depends entirely on MetricsMode. On the ROLLUP family it is
	// nearly free: that family is top-N bounded on (src,dst) pairs regardless
	// of how many dimensions the key carries. On the RAW family with
	// collapse_external on, every external address is one "external" series
	// today and a country label splits it up to ~250 ways — which is what the
	// startup advisory warns about.
	GeoDims bool `yaml:"geo_dims" reload:"restart"`
}

// PerEntityCardinality gates the per-entity gauges of the inventory collectors.
// When a toggle is false, only the low-cardinality aggregate *.count rollup is
// emitted (the per-entity gauges, one series per device/user/key/..., are
// dropped). All default true.
type PerEntityCardinality struct {
	Device  bool `yaml:"device" reload:"restart"`
	User    bool `yaml:"user" reload:"restart"`
	Key     bool `yaml:"key" reload:"restart"`
	Webhook bool `yaml:"webhook" reload:"restart"`
	Service bool `yaml:"service" reload:"restart"`
}

// Collectors groups the per-collector configurations. Each collector exposes
// only the fields that apply to it: the inventory snapshots take just
// enabled+interval (SimpleCollector); the two log collectors add a source and
// windowing fields; devices/keys/services have their own extras.
type Collectors struct {
	Devices             DevicesCollector   `yaml:"devices"`
	Flowlogs            FlowlogsCollector  `yaml:"flowlogs"`
	Auditlogs           AuditlogsCollector `yaml:"auditlogs"`
	K8sAudit            K8sAuditCollector  `yaml:"k8s_audit"`
	Users               SimpleCollector    `yaml:"users"`
	Keys                KeysCollector      `yaml:"keys"`
	Settings            SnapshotCollector  `yaml:"settings"`
	Acl                 AclCollector       `yaml:"acl"`
	Dns                 SnapshotCollector  `yaml:"dns"`
	Contacts            SimpleCollector    `yaml:"contacts"`
	Webhooks            WebhooksCollector  `yaml:"webhooks"`
	PostureIntegrations SnapshotCollector  `yaml:"posture_integrations"`
	LogStream           LogStreamCollector `yaml:"log_stream"`
	Services            ServicesCollector  `yaml:"services"`
	NodeMetrics         NodeMetricsConfig  `yaml:"node_metrics"`
	// OAuthApps is a point-in-time inventory snapshot of the tailnet's OAuth
	// clients (a config-surface seam for #167; the collector itself ships
	// separately).
	OAuthApps SimpleCollector `yaml:"oauth_apps"`
}

// SimpleCollector is a point-in-time inventory collector: it just polls a
// snapshot on its Interval.
type SimpleCollector struct {
	Enabled  bool     `yaml:"enabled" reload:"restart"`
	Interval Duration `yaml:"interval" reload:"restart"`
}

// LogStreamCollector permits the configuration- and network-stream probes to
// inherit the shared interval or override it independently.
type LogStreamCollector struct {
	Enabled               bool     `yaml:"enabled" reload:"restart"`
	Interval              Duration `yaml:"interval" reload:"restart"`
	ConfigurationInterval Duration `yaml:"configuration_interval" reload:"restart"`
	NetworkInterval       Duration `yaml:"network_interval" reload:"restart"`
}

// SnapshotCollector is a point-in-time configuration collector that can also
// emit its complete response as an opt-in structured log snapshot.
type SnapshotCollector struct {
	Enabled         bool     `yaml:"enabled" reload:"restart"`
	Interval        Duration `yaml:"interval" reload:"restart"`
	SnapshotEnabled bool     `yaml:"snapshot_enabled" reload:"restart"`
}

// AclCollector configures the ACL policy collector.
type AclCollector struct {
	Enabled  bool     `yaml:"enabled" reload:"restart"`
	Interval Duration `yaml:"interval" reload:"restart"`
	// SnapshotEnabled is explicit consent to ship the raw policy, including
	// every user email and group member, to the logs backend. It also governs
	// policy diffs. This opt-in overrides pii_filter for those raw bodies, so the
	// configured logs retention holds tailnet identity data.
	SnapshotEnabled bool `yaml:"snapshot_enabled" reload:"restart"`
	// SnapshotHeartbeat refreshes an unchanged policy snapshot so a quiet
	// tailnet remains queryable on a bounded dashboard time range.
	SnapshotHeartbeat Duration `yaml:"snapshot_heartbeat" reload:"restart"`
	// Validate runs the tailnet's active policy through the API's non-mutating
	// POST /tailnet/{tailnet}/acl/validate on each tick, exporting whether the
	// policy still parses and how many embedded ACL tests fail.
	//
	// Despite being a POST this is a READ operation: upstream documents it as
	// requiring only the policy_file:read scope, and it never modifies the
	// policy — it is the one non-GET call this exporter makes. Sending no
	// request body validates the tailnet's CURRENT policy (a body would instead
	// validate a hypothetical replacement, which is not what we want).
	//
	// Default true, on with the collector. Set false to keep the client strictly
	// GET-only. Permission denial surfaces as an unavailable/degraded state, not
	// a healthy zero.
	Validate bool `yaml:"validate" reload:"restart"`
}

// WebhooksCollector configures the webhook-subscription inventory collector.
type WebhooksCollector struct {
	Enabled  bool     `yaml:"enabled" reload:"restart"`
	Interval Duration `yaml:"interval" reload:"restart"`
	// SnapshotEnabled emits the complete webhook inventory response as an
	// opt-in structured log snapshot.
	SnapshotEnabled bool `yaml:"snapshot_enabled" reload:"restart"`
	// DesiredEvents is an optional list of webhook event categories this tailnet
	// is expected to be subscribed to (e.g. "nodeCreated", "userSuspended").
	// When set, the collector reports which desired categories no endpoint
	// covers, so a silently-unsubscribed alerting path becomes visible.
	//
	// Empty (the default) means "no expectation" — coverage is still exported
	// per category, but nothing is reported as missing. Unrecognized values are
	// folded to "other" rather than minting an unbounded label.
	DesiredEvents []string `yaml:"desired_events" reload:"restart"`
}

// DevicesCollector configures the devices collector. Besides the snapshot
// interval it gates the optional routes/posture fetches and the posture log.
type DevicesCollector struct {
	Enabled  bool     `yaml:"enabled" reload:"restart"`
	Interval Duration `yaml:"interval" reload:"restart"`
	// ChangeLogEnabled emits structured device add/remove and field-change
	// records. PII-bearing fields still follow the process pii_filter.
	ChangeLogEnabled bool `yaml:"change_log_enabled" reload:"restart"`
	CollectRoutes    bool `yaml:"collect_routes" reload:"restart"`
	CollectPosture   bool `yaml:"collect_posture" reload:"restart"`
	// CollectDeviceInvites fetches each device's outstanding share invites
	// (GET /device/{id}/device-invites — one API call per device, N+1) and emits
	// the tailscale.device_invites.count aggregate. Requires the
	// device_invites:read OAuth scope (covered by the broad all:read scope).
	// Per-device fetch failures are non-fatal. Default true.
	CollectDeviceInvites bool `yaml:"collect_device_invites" reload:"restart"`
	// SubrequestConcurrency bounds the per-device posture/invite request pool.
	SubrequestConcurrency int `yaml:"subrequest_concurrency" reload:"restart"`
	// PostureComplianceChecks are bounded exact-match checks. A device fails a
	// check when the attribute is missing or differs from Equals.
	PostureComplianceChecks []PostureComplianceCheck `yaml:"posture_compliance_checks" reload:"restart"`
	// PostureLogMode controls the tailscale.device.posture LOG (requires
	// collect_posture): "changes" (default) logs a device only when its posture
	// changes since the last scrape — a full baseline dump on the first scrape,
	// then deltas; "always" logs every scrape; "off" suppresses the log. The
	// posture info-gauge METRIC is emitted every scrape regardless.
	PostureLogMode string `yaml:"posture_log_mode" reload:"restart"`
	// ExpiryLogMode controls both node-key and posture-attribute expiry warnings:
	// daily logs changes plus at most one reminder per 24h, always preserves the
	// legacy every-scrape behavior, and off suppresses logs without suppressing metrics.
	ExpiryLogMode string `yaml:"expiry_log_mode" reload:"restart"`
	// AttributeNamespaces lists the device posture-attribute namespace prefixes (the
	// part before ":" in a posture key, e.g. "intune", "ip") promoted to the
	// tailscale.device.attribute{,.info} metrics (requires collect_posture). The
	// sentinel ["*"] promotes every namespace present; an explicit empty list ([])
	// disables the attribute metrics.
	AttributeNamespaces []string `yaml:"attribute_namespaces" reload:"restart"`
	// AttributeKeyLimit caps distinct posture keys promoted to attribute metrics;
	// over-cap keys are dropped. AttributeValueLimit caps values per key on the
	// info gauge and folds overflow to "__other__". 0 or negative is unlimited;
	// cardinality.metric_limit remains the last-resort SDK backstop.
	AttributeKeyLimit   int `yaml:"attribute_key_limit" reload:"restart"`
	AttributeValueLimit int `yaml:"attribute_value_limit" reload:"restart"`
	// CollectConnectivity (default true) gates the B3 connectivity signals
	// (hard_nat/endpoints/direct_capable/udp/ipv6 + fleet rollups). No extra API
	// calls — read from the rich device payload already fetched.
	CollectConnectivity bool `yaml:"collect_connectivity" reload:"restart"`
	// CollectTagRollup (default true) gates the tailscale.devices.by_tag
	// distribution gauge (one series per ACL tag). When false, only the other
	// fleet-hygiene aggregates (untagged/ephemeral/by_version/key_expiry) emit.
	CollectTagRollup bool `yaml:"collect_tag_rollup" reload:"restart"`
	// TagRollupLimit caps the number of distinct tag series on
	// tailscale.devices.by_tag: the busiest TagRollupLimit tags (by device count)
	// keep their own series; the rest fold into a single tailscale.tag="__other__"
	// series so totals are preserved. Default 50; 0 or negative = unlimited.
	TagRollupLimit int `yaml:"tag_rollup_limit" reload:"restart"`
}

// PostureComplianceCheck is one bounded, operator-named exact-match posture
// assertion. Name becomes a label value, never a metric name.
type PostureComplianceCheck struct {
	Name      string `yaml:"name" reload:"restart"`
	Attribute string `yaml:"attribute" reload:"restart"`
	Equals    string `yaml:"equals" reload:"restart"`
}

// FlowlogsCollector configures the network-flow-logs collector. Source selects
// the ingestion path (poll/stream/both); the windowing fields apply only when
// polling.
type FlowlogsCollector struct {
	Enabled         bool     `yaml:"enabled" reload:"restart"`
	Source          string   `yaml:"source" reload:"restart"`
	Interval        Duration `yaml:"interval" reload:"restart"`         // poll only
	Lag             Duration `yaml:"lag" reload:"restart"`              // poll only
	InitialLookback Duration `yaml:"initial_lookback" reload:"restart"` // poll only
	MaxWindow       Duration `yaml:"max_window" reload:"restart"`       // poll only
	// DedupCapacity bounds both poll-window and cross-source connection identity
	// sets. It must stay positive; an unbounded dedup set is a memory leak.
	DedupCapacity int `yaml:"dedup_capacity" reload:"restart"`
	// ReplayOverlap rereads this much of the most recently completed poll
	// window so records that became available late can still be accepted.
	// ReplaySeenCapacity bounds the durable connection identities retained to
	// suppress the intentional overlap across process restarts.
	ReplayOverlap      Duration `yaml:"replay_overlap" reload:"restart"`       // poll only
	ReplaySeenCapacity int      `yaml:"replay_seen_capacity" reload:"restart"` // poll only
	// TrustedReporterNodeIDs and TrustedReporterTags define the operator's
	// allowlist for the verified FlowLog.NodeID reporter. Tag trust is resolved
	// only from the authoritative device cache; tags embedded in a flow record
	// never grant trust.
	TrustedReporterNodeIDs []string `yaml:"trusted_reporter_node_ids" reload:"restart"`
	TrustedReporterTags    []string `yaml:"trusted_reporter_tags" reload:"restart"`
	// LogMode sets the per-connection/per-record/off log detail (applies to poll
	// AND stream).
	LogMode string `yaml:"log_mode" reload:"restart"`
	// MaxLogRecordsPerWindow caps flow LOG records emitted per poll window (0 =
	// unlimited). Excess is counted into tailscale.network.flow.logs_dropped;
	// metrics are never capped.
	MaxLogRecordsPerWindow int `yaml:"max_log_records_per_window" reload:"restart"`
	// ObjectStore configures the objectstore ingestion path. It applies only
	// when source includes "objectstore".
	ObjectStore ObjectStoreConfig `yaml:"objectstore"`
}

// ObjectStoreConfig points the objectstore ingestion path at the S3-compatible
// bucket Tailscale exports network flow logs into.
//
// Credentials are deliberately last-resort here: leave them empty and the
// ambient chain is used (the environment, then IRSA/web identity, then an EC2
// instance profile), which is what a container deployment should rely on. Set
// them ONLY via TS2OTEL_* environment variables, never in YAML.
type ObjectStoreConfig struct {
	// Endpoint is the service URL, e.g. https://s3.eu-west-2.amazonaws.com, or a
	// MinIO/Ceph address. There is no region-to-endpoint guessing: the non-AWS
	// implementations this supports would all be guessed wrong.
	Endpoint string `yaml:"endpoint" reload:"restart"`
	Region   string `yaml:"region" reload:"restart"`
	Bucket   string `yaml:"bucket" reload:"restart"`
	// Prefix is the export's root within the bucket, above the YYYY/MM/DD
	// partitions.
	Prefix string `yaml:"prefix" reload:"restart"`
	// Layout is how objects are arranged under Prefix:
	// ObjectStoreLayoutPartitioned (the default, and what Tailscale itself
	// writes) or ObjectStoreLayoutFlat for a copied/mirrored export whose
	// self-contained basenames sit directly under Prefix. Empty means
	// partitioned; there is deliberately no autodetection, because the two are
	// distinguishable only by listing the bucket and guessing wrong changes what
	// the durable scan positions mean.
	Layout string `yaml:"layout" reload:"restart"`
	// PathStyle addresses the bucket as <endpoint>/<bucket>/<key> rather than
	// <bucket>.<endpoint>/<key>. Required by most non-AWS implementations.
	PathStyle bool `yaml:"path_style" reload:"restart"`
	// AllowInsecureHTTP permits a plaintext remote endpoint. Plain HTTP is
	// otherwise accepted only for loopback development endpoints.
	AllowInsecureHTTP bool `yaml:"allow_insecure_http" reload:"restart"`
	// Static credentials. Prefer workload identity. Each value has a
	// Docker-secrets-style file sibling; set the value or the file, never both.
	AccessKeyID     Secret `yaml:"access_key_id" reload:"restart"`
	AccessKeyIDFile string `yaml:"access_key_id_file" reload:"restart"`

	SecretAccessKey     Secret `yaml:"secret_access_key" reload:"restart"`
	SecretAccessKeyFile string `yaml:"secret_access_key_file" reload:"restart"`

	SessionToken     Secret `yaml:"session_token" reload:"restart"`
	SessionTokenFile string `yaml:"session_token_file" reload:"restart"`
	// Interval is how often the bucket is listed.
	Interval Duration `yaml:"interval" reload:"restart"`
	// Lookback is how far back past the cursor each listing reaches, so an
	// object that arrived late is still found. The ingested-object guard is what
	// keeps the overlap from re-ingesting.
	Lookback Duration `yaml:"lookback" reload:"restart"`
	// InitialLookback bounds a cold start against a bucket holding a long
	// history.
	InitialLookback Duration `yaml:"initial_lookback" reload:"restart"`
	// MaxObjects bounds one cycle's work. Exceeding it is not an error: the
	// remainder is counted, logged and picked up next cycle.
	MaxObjects int `yaml:"max_objects" reload:"restart"`
	// MaxSeenKeys bounds durable seen-object identities per destination. It must
	// stay positive; too small a value can re-admit an evicted object as new.
	MaxSeenKeys int `yaml:"max_seen_keys" reload:"restart"`
	// MaxObjectWireBytes, MaxObjectDecompressedBytes, and MaxObjectRecords bound
	// one exported object's input and expansion before any records are committed.
	MaxObjectWireBytes         int64 `yaml:"max_object_wire_bytes" reload:"restart"`
	MaxObjectDecompressedBytes int64 `yaml:"max_object_decompressed_bytes" reload:"restart"`
	MaxObjectRecords           int   `yaml:"max_object_records" reload:"restart"`
	// MaxCycleWireBytes, MaxCycleDecompressedBytes, and MaxCycleRecords bound
	// aggregate input and decode work across one collection cycle. Each must be
	// at least its corresponding per-object bound.
	MaxCycleWireBytes         int64 `yaml:"max_cycle_wire_bytes" reload:"restart"`
	MaxCycleDecompressedBytes int64 `yaml:"max_cycle_decompressed_bytes" reload:"restart"`
	MaxCycleRecords           int   `yaml:"max_cycle_records" reload:"restart"`
}

// AuditlogsCollector configures the configuration/audit-events collector. Source
// selects the ingestion path; the windowing fields apply only when polling.
type AuditlogsCollector struct {
	Enabled         bool     `yaml:"enabled" reload:"restart"`
	Source          string   `yaml:"source" reload:"restart"`
	Interval        Duration `yaml:"interval" reload:"restart"`         // poll only
	Lag             Duration `yaml:"lag" reload:"restart"`              // poll only
	InitialLookback Duration `yaml:"initial_lookback" reload:"restart"` // poll only
	MaxWindow       Duration `yaml:"max_window" reload:"restart"`       // poll only
	// DedupCapacity bounds both poll-window and cross-source audit/webhook event
	// identity sets. It must stay positive; an unbounded set is a memory leak.
	DedupCapacity int `yaml:"dedup_capacity" reload:"restart"`
	// ObjectStore configures the objectstore ingestion path for Tailscale's
	// CONFIGURATION-log export. It applies only when source is "objectstore", and
	// it is a destination of its own: the configuration and network exports are
	// separate, so nothing here is shared with or inherited from
	// collectors.flowlogs.objectstore.
	ObjectStore ObjectStoreConfig `yaml:"objectstore"`
}

// K8sAuditCollector configures the tsrecorder Kubernetes API-audit collector:
// Kubernetes-API-proxy audit events (.event) and terminal-session headers
// (.cast) tsrecorder writes to its own S3-compatible bucket.
//
// Unlike FlowlogsCollector/AuditlogsCollector there is deliberately NO Source
// field. Object storage is the ONLY consumption surface tsrecorder exposes —
// there is no control-plane API and no push — so a poll/stream toggle would
// offer a choice that can never be honored.
type K8sAuditCollector struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
	// ObjectStore configures the objectstore ingestion path. It is a
	// destination of its own: tsrecorder writes to a different bucket, with a
	// different key layout, from both the network (flowlogs) and
	// configuration (auditlogs) exports, so nothing here is shared with or
	// inherited from collectors.flowlogs.objectstore or
	// collectors.auditlogs.objectstore.
	ObjectStore ObjectStoreConfig `yaml:"objectstore"`
}

// KeysCollector configures the keys collector. ExpiryWarn sets how far ahead of
// a key's expiry the WARN log fires.
type KeysCollector struct {
	Enabled    bool     `yaml:"enabled" reload:"restart"`
	Interval   Duration `yaml:"interval" reload:"restart"`
	ExpiryWarn Duration `yaml:"expiry_warn" reload:"restart"`
	// ExpiryLogMode controls expiry warning cadence: daily logs changes plus at
	// most one reminder per 24h, always preserves every-scrape behavior, and off
	// suppresses logs without suppressing metrics.
	ExpiryLogMode string `yaml:"expiry_log_mode" reload:"restart"`
}

// ServicesCollector configures the Tailscale Services (VIP) collector.
// CollectHosts adds per-service backing-host detail — one extra API call per
// service (N+1). Off by default. CollectTagRollup emits a bounded per-ACL-tag
// service count, with TagRollupLimit matching the devices collector's busiest-N
// plus __other__ behavior.
type ServicesCollector struct {
	Enabled               bool     `yaml:"enabled" reload:"restart"`
	Interval              Duration `yaml:"interval" reload:"restart"`
	CollectHosts          bool     `yaml:"collect_hosts" reload:"restart"`
	CollectTagRollup      bool     `yaml:"collect_tag_rollup" reload:"restart"`
	TagRollupLimit        int      `yaml:"tag_rollup_limit" reload:"restart"`
	SubrequestConcurrency int      `yaml:"subrequest_concurrency" reload:"restart"`
}

// SchedulerConfig controls process-wide first-tick staggering.
type SchedulerConfig struct {
	InitialStaggerWindow Duration `yaml:"initial_stagger_window" reload:"restart"`
}

// NodeMetricsConfig configures the optional node-local metrics scraper, which
// scrapes a configured list of Prometheus-text /metrics endpoints (e.g.
// tailscaled per-node metrics) and re-emits them centrally. It is off by
// default and disabled when no targets are configured. Node identity is carried
// as a label, not as an OTEL Resource.
type NodeMetricsConfig struct {
	Enabled   bool                 `yaml:"enabled" reload:"restart"`
	Interval  Duration             `yaml:"interval" reload:"restart"`
	Timeout   Duration             `yaml:"timeout" reload:"restart"`
	Targets   []NodeMetricsTarget  `yaml:"targets" reload:"restart"`
	Discovery NodeMetricsDiscovery `yaml:"discovery"`

	// Scrape limits bound memory and telemetry cardinality per target.
	MaxResponseBytes int64 `yaml:"max_response_bytes" reload:"restart"` // maximum response bytes read from one scrape
	MaxSamples       int   `yaml:"max_samples" reload:"restart"`        // maximum valid samples forwarded from one scrape
	// MaxDistinctMetrics bounds the number of DISTINCT forwarded metric names
	// over the process lifetime. MaxSamples caps one scrape; a scrape target
	// picks its own metric names, and every unseen name creates an OTEL
	// instrument that is never released, so without this budget a compromised
	// target grows the instrument registry without limit. Names beyond the
	// budget are dropped and counted (never silently); 0 selects a default of
	// 2000, negative disables the budget.
	MaxDistinctMetrics int `yaml:"max_distinct_metrics" reload:"restart"`

	// Passthrough filters on the FORWARDED Prometheus samples. They never affect
	// tailscale.node.up or the discovery.* gauges. A zero value means no filtering.
	MetricAllow []string `yaml:"metric_allow" reload:"restart"` // anchored regex on the forwarded metric NAME; if non-empty, a name must match one to be forwarded
	MetricDeny  []string `yaml:"metric_deny" reload:"restart"`  // anchored regex; a name matching any is dropped (applied after allow)
	DropLabels  []string `yaml:"drop_labels" reload:"restart"`  // label keys stripped from every forwarded series (the `instance` label is never dropped)
}

// NodeMetricsDiscovery configures DYNAMIC scrape-target discovery from the
// Tailscale devices API. When enabled, the live device inventory is polled on
// this block's own Interval (default 5m, independent of the scrape Interval) and
// each matching device becomes a scrape target; discovered targets are UNIONED
// (deduped by URL) with the static Targets list, so existing static-only configs
// are unaffected. Reachability is reported by tailscale.node.up=0 for any node
// the scraper cannot reach (no ACL parsing is performed).
type NodeMetricsDiscovery struct {
	Enabled  bool     `yaml:"enabled" reload:"restart"`
	Interval Duration `yaml:"interval" reload:"restart"`

	// Metrics-endpoint shape applied to every discovered device.
	Scheme string `yaml:"scheme" reload:"restart"` // "http" (default) | "https"
	Port   int    `yaml:"port" reload:"restart"`   // default 5252 (tailscaled client metrics)
	Path   string `yaml:"path" reload:"restart"`   // default "/metrics"

	// PortOverrides maps a Tailscale tag to the metrics ports served by devices
	// carrying it. A device matching at least one key is scraped on the
	// deduplicated union of those keys' ports INSTEAD OF Port, one target per
	// port. A device matching no key uses Port exactly as before. File-only: a
	// map-valued key cannot be set through the TS2OTEL_* environment convention.
	PortOverrides map[string][]int `yaml:"port_overrides" reload:"restart"`

	// Filters.
	MaxTargets      int      `yaml:"max_targets" reload:"restart"`      // maximum discovered targets per refresh
	OnlineOnly      bool     `yaml:"online_only" reload:"restart"`      // default true: only connectedToControl devices
	ExcludeExternal bool     `yaml:"exclude_external" reload:"restart"` // default true: skip shared/external devices
	IncludeTags     []string `yaml:"include_tags" reload:"restart"`     // empty = match all; any-match
	ExcludeTags     []string `yaml:"exclude_tags" reload:"restart"`     // wins over include_tags

	// Address + instance selection.
	AddressOrder   string `yaml:"address_order" reload:"restart"`   // "ipv4" (default) | "ipv6" (preferred family; falls back to the other)
	InstanceSource string `yaml:"instance_source" reload:"restart"` // node identity label: "address" (default, host:port) | "name" (MagicDNS short name, unique) | "hostname" (OS hostname; NOT unique — collisions like "localhost" are disambiguated by address + WARN)

	// Passthrough labels merged onto each discovered target's series, for
	// join-ability with tailscale.device.* (host.name/host.id, tailscale.tags).
	IncludeHostLabels bool `yaml:"include_host_labels" reload:"restart"` // default true
	IncludeTagsLabel  bool `yaml:"include_tags_label" reload:"restart"`  // default true
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
	URL             string            `yaml:"url" reload:"restart"`
	Instance        string            `yaml:"instance" reload:"restart"`
	Labels          map[string]string `yaml:"labels" reload:"restart"`
	BearerToken     Secret            `yaml:"bearer_token" reload:"restart"`
	BearerTokenFile string            `yaml:"bearer_token_file" reload:"restart"`
	// Headers values are Secret so a per-target scrape credential passed as a
	// custom header (e.g. X-Scope-OrgID or an Authorization token) redacts itself
	// in any config dump/log (#73).
	Headers map[string]Secret     `yaml:"headers" reload:"restart"`
	TLS     *NodeMetricsTargetTLS `yaml:"tls"`
}

// NodeMetricsTargetTLS is the optional per-target TLS trust/identity for HTTPS
// node-metrics targets. InsecureSkipVerify defaults to false (a footgun guard).
type NodeMetricsTargetTLS struct {
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify" reload:"restart"`
	CAFile             string `yaml:"ca_file" reload:"restart"`
	CertFile           string `yaml:"cert_file" reload:"restart"`
	KeyFile            string `yaml:"key_file" reload:"restart"`
	ServerName         string `yaml:"server_name" reload:"restart"`
}

// CheckpointConfig configures poll-cursor and semantic-evidence persistence.
type CheckpointConfig struct {
	// Store selects persistence for disposable poll high-water marks.
	Store string `yaml:"store" reload:"restart"`
	// EvidenceStore independently selects persistence for restart-stable facts
	// such as ACL revision provenance. Both file-backed classes share FilePath.
	EvidenceStore string `yaml:"evidence_store" reload:"restart"`
	FilePath      string `yaml:"file_path" reload:"restart"`
	// WriteDebounce coalesces nearby file writes. Zero preserves synchronous Set.
	WriteDebounce Duration `yaml:"write_debounce" reload:"restart"`
}

// IngressWALConfig configures the process-global write-ahead log for accepted
// streaming and webhook request bodies. It is disabled by default.
type IngressWALConfig struct {
	Enabled    bool   `yaml:"enabled" reload:"restart"`
	Directory  string `yaml:"directory" reload:"restart"`
	MaxBytes   int64  `yaml:"max_bytes" reload:"restart"`
	MaxEntries int    `yaml:"max_entries" reload:"restart"`
	Corruption string `yaml:"corruption" reload:"restart"`
}

// StreamingConfig configures the HEC-style streaming receiver.
type StreamingConfig struct {
	Enabled bool   `yaml:"enabled" reload:"restart"`
	Listen  string `yaml:"listen" reload:"restart"`
	Path    string `yaml:"path" reload:"restart"`
	Token   Secret `yaml:"token" reload:"restart"`
	// TokenFile reads Token from a file at Load (Docker-secrets style). Value XOR
	// file: setting both is a Validate error. The file content is trimmed of
	// surrounding whitespace before use.
	TokenFile string `yaml:"token_file" reload:"file_content"`
	// PublicURL is the externally reachable URL Tailscale should POST logs to
	// (this receiver's public endpoint). Required only when AutoConfigure is on,
	// since it is the sink URL registered with Tailscale.
	PublicURL  string       `yaml:"public_url" reload:"restart"`
	TLS        StreamingTLS `yaml:"tls"`
	Decompress string       `yaml:"decompress" reload:"restart"`
	// AutoConfigure, when true, PUTs this receiver as a Splunk-HEC log-streaming
	// sink on startup (requires Enabled and PublicURL). Off by default.
	AutoConfigure bool `yaml:"auto_configure" reload:"restart"`
	// MaxBodyBytes caps the DECOMPRESSED request body; an over-cap POST is
	// rejected with 413 + rejected{reason=too_large} so a huge or zip-bomb body
	// cannot OOM the receiver. 0 selects a 64 MiB default; negative disables it.
	MaxBodyBytes int64 `yaml:"max_body_bytes" reload:"restart"`
	// MaxConcurrentRequests bounds how many requests may buffer a body at once,
	// so N simultaneous in-limit POSTs cannot sum past the process memory budget
	// (#209). MaxBodyBytes caps ONE body; this caps their sum. An over-limit POST
	// is rejected with 503 + Retry-After + rejected{reason=overloaded}. 0 selects
	// a default of 4; negative disables the limit. Raise it only alongside the
	// process memory limit: the worst case is roughly this times max_body_bytes.
	MaxConcurrentRequests int `yaml:"max_concurrent_requests" reload:"restart"`
	// PerRouteMaxConcurrentRequests bounds any one route. Zero selects an
	// automatic fair share of the global admission budget.
	PerRouteMaxConcurrentRequests int `yaml:"per_route_max_concurrent_requests" reload:"restart"`
	// Routes is the FILE-ONLY multi-tailnet receiver map. When non-empty it
	// replaces the legacy path/token/public_url identity fields above; listener,
	// TLS, decompression and resource limits remain process-wide.
	Routes []StreamingRoute `yaml:"routes" reload:"restart"`
}

// StreamingRoute identifies one multi-tailnet HEC destination. A route owns
// the exact HTTP path and token used by that tailnet; it deliberately has no
// listener or TLS knobs because all routes share one bounded HTTP server.
type StreamingRoute struct {
	Tailnet       string `yaml:"tailnet" reload:"restart"`
	Path          string `yaml:"path" reload:"restart"`
	Token         Secret `yaml:"token" reload:"restart"`
	TokenFile     string `yaml:"token_file" reload:"file_content"`
	PublicURL     string `yaml:"public_url" reload:"restart"`
	AutoConfigure bool   `yaml:"auto_configure" reload:"restart"`
}

// StreamingTLS configures TLS for the streaming receiver.
type StreamingTLS struct {
	CertFile string `yaml:"cert_file" reload:"file_content"`
	KeyFile  string `yaml:"key_file" reload:"file_content"`
}

// WebhookConfig configures the inbound webhook receiver.
type WebhookConfig struct {
	Enabled bool   `yaml:"enabled" reload:"restart"`
	Listen  string `yaml:"listen" reload:"restart"`
	Path    string `yaml:"path" reload:"restart"`
	Secret  Secret `yaml:"secret" reload:"restart"`
	// TLS optionally serves the webhook listener over HTTPS. It reuses the
	// streaming receiver's paired cert/key contract; leaving both empty keeps
	// plaintext HTTP for reverse-proxy deployments.
	TLS StreamingTLS `yaml:"tls"`
	// SecretFile reads Secret from a file at Load (Docker-secrets style). Value
	// XOR file: setting both is a Validate error. The file content is trimmed of
	// surrounding whitespace before use.
	SecretFile string `yaml:"secret_file" reload:"file_content"`
	// Tolerance is the maximum age of a webhook's signed timestamp before it is
	// rejected as a replay. Tailscale signs "<unix>.<body>", so this bounds how
	// long a captured, validly-signed delivery can be replayed. 0 disables the
	// check; defaults to 5m.
	Tolerance Duration `yaml:"tolerance" reload:"restart"`
	// DedupAuditEvents, when true, shares a best-effort cross-source de-dup set
	// with the audit processor so a change reported by BOTH a webhook and the
	// audit logs is counted once. Off by default.
	DedupAuditEvents bool `yaml:"dedup_audit_events" reload:"restart"`
	// MaxBodyBytes caps the raw request body read before signature verification
	// (the HMAC covers the whole body, so some pre-auth buffering is
	// unavoidable); an over-cap POST is rejected with 413 +
	// rejected{reason=too_large}, mirroring streaming.max_body_bytes. 0 selects
	// a 1 MiB default (real Tailscale webhook payloads are KB-scale); a
	// negative value disables the cap.
	MaxBodyBytes int64 `yaml:"max_body_bytes" reload:"restart"`
	// MaxConcurrentRequests bounds how many requests may buffer a body at once,
	// mirroring streaming.max_concurrent_requests (#209). The HMAC covers the
	// whole body, so buffering happens BEFORE any credential is verified: without
	// an aggregate bound, N unauthenticated senders multiply MaxBodyBytes. An
	// over-limit POST is rejected with 503 + Retry-After +
	// rejected{reason=overloaded} before the body is read. 0 selects a default of
	// 4; negative disables the limit. Worst-case buffered memory is roughly this
	// times max_body_bytes.
	MaxConcurrentRequests         int `yaml:"max_concurrent_requests" reload:"restart"`
	PerRouteMaxConcurrentRequests int `yaml:"per_route_max_concurrent_requests" reload:"restart"`
	// Routes is the FILE-ONLY multi-tailnet receiver map. The legacy path and
	// secret fields remain the single-tailnet compatibility surface.
	Routes []WebhookRoute `yaml:"routes" reload:"restart"`
}

// WebhookRoute identifies the tailnet whose signed deliveries a secret
// authenticates. All routes share webhook.listen/path and the existing body /
// admission limits.
type WebhookRoute struct {
	Tailnet    string `yaml:"tailnet" reload:"restart"`
	Secret     Secret `yaml:"secret" reload:"restart"`
	SecretFile string `yaml:"secret_file" reload:"file_content"`
}

// TracingConfig configures the OTEL traces pillar. Off by default; reuses otlp.*
// for the endpoint/protocol/headers/TLS.
type TracingConfig struct {
	Enabled    bool    `yaml:"enabled" reload:"restart"`
	Sampler    string  `yaml:"sampler" reload:"restart"`     // always_on|always_off|traceidratio|parentbased_always_on|parentbased_traceidratio
	SamplerArg float64 `yaml:"sampler_arg" reload:"restart"` // ratio in [0,1] for the *traceidratio samplers

	// Samplers optionally overrides the head sampler per workload class (#372).
	// High-rate receiver traffic can otherwise drown out the low-rate collector
	// failures that are the more useful traces. An unset class inherits Sampler /
	// SamplerArg above, so the zero value is exactly today's single global
	// sampler. Error- and latency-aware policies belong in Alloy's tail-sampling
	// processor, not here.
	Samplers TracingSamplers `yaml:"samplers"`

	// RemoteParent is how an inbound W3C traceparent's sampled bit is treated by
	// the stream and webhook receivers (#373): trust it (the default, and today's
	// behavior), ignore it so the local sampler alone decides, or convert it to a
	// link on a fresh local root trace. Without this, an authenticated sender's
	// sampled bit overrides the local ratio.
	RemoteParent string `yaml:"remote_parent" reload:"restart"` // trust|ignore|link
}

// TracingSamplers holds the per-class head-sampler overrides. The three classes
// are a closed set so the sampler's own decision dimensions stay bounded.
type TracingSamplers struct {
	Scrape     TracingSamplerClass `yaml:"scrape"`
	Receiver   TracingSamplerClass `yaml:"receiver"`
	Background TracingSamplerClass `yaml:"background"`
}

// TracingSamplerClass is one class's sampler override. An empty Sampler means
// "inherit the global tracing.sampler".
type TracingSamplerClass struct {
	Sampler string  `yaml:"sampler" reload:"restart"`
	Arg     float64 `yaml:"arg" reload:"restart"`
}

// ResourceConfig is opt-in enrichment of the OTEL Resource (#380).
//
// This is deliberately narrow and bounded. The metrics Resource is a per-series
// label surface on Grafana Cloud (it promotes the whole service.* namespace), so
// anything added here can multiply active-series cardinality — which is why the
// app's own identity always wins, service.version is still kept off the metrics
// Resource (#187), tailscale.tailnet / tailscale2otel.provider stay signal-scoped
// attributes, and reading the ambient environment is off by default.
type ResourceConfig struct {
	// ServiceNamespace sets service.namespace. Grafana Cloud promotes it to a
	// per-series label alongside job, so it must be low-cardinality and stable
	// across deploys.
	ServiceNamespace string `yaml:"service_namespace" reload:"restart"`
	// DeploymentEnvironment sets deployment.environment.name. Outside the
	// service.* namespace, so it lands in target_info rather than on every
	// series, and may safely vary per environment.
	DeploymentEnvironment string `yaml:"deployment_environment" reload:"restart"`
	// Attributes are bounded custom Resource attributes (deploy/ownership tags).
	// Reserved keys are refused, not silently accepted.
	Attributes map[string]string `yaml:"attributes" reload:"restart"`
	// FromEnv additionally reads OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME,
	// filtered by the same rules. Default false: it hands the ambient deployment
	// environment a channel onto a per-series label surface, which should be a
	// deliberate choice rather than something inherited.
	FromEnv bool `yaml:"from_env" reload:"restart"`
}

// CredentialReloadConfig turns on rotation of outbound credential and TLS files
// without a restart (#362). Secret and mTLS rotation in Kubernetes or Docker
// otherwise costs a process restart and a telemetry gap.
//
// Enabled governs only the background POLLER. The reloader is constructed
// whenever any watched file is configured, so a malformed replacement is always
// caught and the last known-good material always retained, poller or not.
type CredentialReloadConfig struct {
	Enabled  bool     `yaml:"enabled" reload:"restart"`
	Interval Duration `yaml:"interval" reload:"restart"`
}

// SpanProfilesConfig is opt-in Pyroscope span-profile correlation (#370): CPU
// profiles become reachable from a Grafana trace-to-profile link. CPU only —
// Go's runtime attaches pprof labels to CPU samples, so heap, mutex, block and
// goroutine profiles cannot carry span identity.
type SpanProfilesConfig struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
}

// PIIFilterConfig controls which PII / identifier categories are emitted.
// All categories default to true (emitted); set a category to false to drop
// those identifiers from metrics and logs at runtime (opt-out redaction).
type PIIFilterConfig struct {
	Emails           bool `yaml:"emails" reload:"restart"`
	UserDisplayNames bool `yaml:"user_display_names" reload:"restart"`
	UserIDs          bool `yaml:"user_ids" reload:"restart"`
	Hostnames        bool `yaml:"hostnames" reload:"restart"`
	NodeIDs          bool `yaml:"node_ids" reload:"restart"`
	TailscaleIPs     bool `yaml:"tailscale_ips" reload:"restart"`
	InternalIPs      bool `yaml:"internal_ips" reload:"restart"`
	ExternalIPs      bool `yaml:"external_ips" reload:"restart"`
	ServiceAddrs     bool `yaml:"service_addrs" reload:"restart"`
	EndpointPaths    bool `yaml:"endpoint_paths" reload:"restart"`
	NetworkTopology  bool `yaml:"network_topology" reload:"restart"`
	TailnetName      bool `yaml:"tailnet_name" reload:"restart"`
	FreeTextDetails  bool `yaml:"free_text_details" reload:"restart"`
	// CommandText controls the verbatim `kubectl exec` command line on
	// Kubernetes-audit logs. It is separate from FreeTextDetails because it is
	// the only attribute typed by a human at a shell, so it can carry a pasted
	// secret. Turning it off keeps the bounded command_class classification,
	// which is what the exec metrics are built on.
	CommandText bool `yaml:"command_text" reload:"restart"`
}

// SelfObservabilityConfig toggles emitting the collector's own telemetry.
type SelfObservabilityConfig struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
	// InstanceID sets the service.instance.id resource attribute so multiple
	// instances of the exporter are distinguishable in the backend. When empty it
	// falls back to the host name (see internal/app instanceID). Set it from the
	// environment, e.g. TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID=$POD_NAME.
	InstanceID string `yaml:"instance_id" reload:"restart"`
}

// Load builds the configuration by layering, lowest precedence first: built-in
// defaults, an optional YAML file at path (skipped when path is ""), and
// TS2OTEL_* environment variables. The merged result is validated before it is
// returned. A non-empty path that cannot be read is an error; absence of a path
// is not (defaults + environment are sufficient to run).
func Load(path string) (*Config, error) {
	if hits := fileOnlyMapSliceEnvVars(); len(hits) > 0 {
		return nil, fileOnlyMapSliceEnvVarError(hits)
	}

	// 0. Reject any TS2OTEL_* variable that indexes into a list-of-structs
	//    config key (e.g. TS2OTEL_TAILNETS__0__NAME or
	//    TS2OTEL_COLLECTORS__NODE_METRICS__TARGETS__0__URL) upfront, before it
	//    reaches mapstructure — which would otherwise silently decode it into a
	//    slice holding a mostly-empty struct, dropping the intended value (see
	//    structSliceEnvKeys and #79). This check is independent of the file/env
	//    layering below since it only inspects variable names.
	if hits := structSliceIndexEnvVars(); len(hits) > 0 {
		return nil, structSliceEnvVarError(hits)
	}

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
	var deviceVersionChecksExplicit bool
	if path != "" {
		configData, info, err := safefile.ReadRegularInfo(path, safefile.MaxConfigBytes, safefile.AllowSymlink)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		provider := configBytesProvider(configData)
		if err := k.Load(provider, yaml.Parser()); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		// 2b. Reject any key in the file that isn't a known config key (or a
		//     child of a known collection key, e.g. otlp.headers.x_org) — a
		//     hard error, unlike the advisory-only unknownEnvVars below (#303).
		//     Loaded into a SEPARATE koanf instance so fk.Keys() reflects only
		//     the file's own keys, not the defaults layered on top of it.
		fk := koanf.New(keyDelim)
		if err := fk.Load(provider, yaml.Parser()); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		if u := unknownFileKeys(fk.Keys(), validKeys); len(u) > 0 {
			return nil, unknownKeyError(u, validKeys)
		}
		deviceVersionChecksExplicit = fk.Exists("version_checks.devices.enabled")
		if info.Mode().Perm()&0o044 != 0 {
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
	if _, set := envSetKeys()["version_checks.devices.enabled"]; set {
		deviceVersionChecksExplicit = true
	}
	if cfg.Provider == "headscale" && !deviceVersionChecksExplicit {
		// The built-in default is useful for a Tailscale fleet, but comparing
		// Headscale devices against Tailscale stable is meaningless. Preserve an
		// explicit true or false from YAML/environment as the operator's choice.
		cfg.VersionChecks.Devices.Enabled = false
	}
	if err := applyTailnetEnvOverlays(&cfg); err != nil {
		return nil, err
	}

	cfg.unknownEnv = unknownEnvVars(validKeys)
	cfg.configFileWarning = cfgFileWarning

	// 4. Resolve every path-bearing field (#310) against the YAML file's own
	//    directory when it supplied a relative path; a path set via environment
	//    keeps CWD semantics, and an absolute path (from either layer) is used
	//    as-is. Done before resolveSecretFiles / Validate so both open the same
	//    resolved path this process will actually use at runtime, and can name
	//    it (alongside what the operator wrote) on failure. See paths.go.
	cfg.pathResolutions = resolveConfigPaths(&cfg, path, envSetKeys())

	// 5. Resolve every *_file secret sibling (Docker-secrets style) now that file
	//    + env layering is complete AND every such field's path has already been
	//    resolved per #310: read each configured file once, trim it, and
	//    populate the paired Secret value. Done before Validate so a bad/missing
	//    file surfaces as a Load error, and so Validate sees the resolved value.
	if err := cfg.resolveSecretFiles(); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		// The config is returned ALONGSIDE the error, unlike every failure above
		// it. Those are decode failures, where the value is partial garbage and
		// handing it back would invite acting on it. This one is different: the
		// file decoded cleanly and only a rule rejected it, so the value is
		// complete and Diagnostics() can report every OTHER problem in the same
		// pass — which is the whole point of #307, and impossible if the only
		// thing that survives is the first error.
		//
		// Safe for existing callers: all of them branch on err != nil and never
		// look at the config after that.
		return &cfg, err
	}
	return &cfg, nil
}
