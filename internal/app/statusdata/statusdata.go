// Package statusdata defines the data model rendered by the admin status page
// (internal/app/statushtml) and served verbatim as JSON at /api/status.json.
// Keeping it a dependency-free leaf lets the HTML and JSON paths share a single
// source of truth so the two can never drift. All fields are plain values;
// durations are pre-rendered as both a numeric (seconds/ms) and a human string,
// and times as RFC3339 strings, so neither consumer needs formatting logic.
package statusdata

// Status is the full admin status snapshot.
type Status struct {
	Service ServiceInfo `json:"service"`
	// Provider is the control-plane backend: "tailscale" or "headscale".
	Provider string `json:"provider"`
	// Capabilities lists the collector/feature names supported by the active provider.
	Capabilities []string `json:"capabilities,omitempty"`
	// Health is the at-a-glance verdict: "healthy", "degraded" or "starting".
	// HealthReasons explains a non-healthy verdict (empty when healthy).
	Health        string        `json:"health"`
	HealthReasons []string      `json:"health_reasons,omitempty"`
	Telemetry     TelemetryInfo `json:"telemetry"`
	// Tailnets is the per-tailnet breakdown (one entry per observed tailnet;
	// length 1 in single-tailnet / Headscale mode). The top-level Collectors/
	// Cache/Devices/API fields carry the COMBINED view across all tailnets.
	Tailnets      []TailnetStatus   `json:"tailnets"`
	Collectors    []CollectorStatus `json:"collectors"`
	Cache         CacheInfo         `json:"device_cache"`
	RDNS          RDNSInfo          `json:"reverse_dns"`
	Dedup         []DedupInfo       `json:"dedup_sets"`
	Devices       []DeviceRow       `json:"devices"`
	NodeDiscovery NodeDiscovery     `json:"node_discovery"`
	Cardinality   CardinalityInfo   `json:"cardinality"`
	Flows         FlowStoreInfo     `json:"flow_store"`
	Receivers     ReceiversInfo     `json:"receivers"`
	// Components is every long-running non-collector subsystem and whether it
	// has failed, from the same state /readyz reads (#318).
	Components []ComponentStatus `json:"components"`
	// Delivery is what the OTLP exporters actually shipped, per signal (#317).
	// Distinct from Throughput, which counts what was HANDED to them.
	Delivery   []DeliverySignal `json:"delivery"`
	Profiling  ProfilingInfo    `json:"profiling"`
	Runtime    RuntimeInfo      `json:"runtime"`
	Throughput ThroughputInfo   `json:"throughput"`
	Fleet      FleetInfo        `json:"fleet"`
	API        APIInfo          `json:"api"`
	Metrics    []MetricRow      `json:"metrics"`
	LogEvents  []LogRow         `json:"log_events"`
	Config     ConfigSummary    `json:"config"`
	// CapabilityMatrix is the configuration-to-capability matrix (#425/#430): one
	// row per enabled collector (plus its optional per-entity subrequests) joining
	// the required API capability/scope, the provider's support for it, the live
	// permission state, and the last probe. Deliberately a flat list so the JSON
	// is directly machine-readable.
	CapabilityMatrix []CapabilityRow `json:"capability_matrix"`
	GeneratedAt      string          `json:"generated_at"`
	// RefreshMs is the client poll interval in milliseconds (admin.status_refresh_interval).
	// The page falls back to 5000 when this is 0. The 1s freshness ticker is independent.
	RefreshMs int `json:"refresh_ms,omitempty"`
}

// TailnetStatus is one tailnet's section of the status page: its identity, auth
// method, and that tailnet's device cache, collector health, devices, and API
// health. (Process-global info — uptime, build, export, runtime — stays in the
// top-level Status header.)
type TailnetStatus struct {
	Name       string            `json:"name"`
	AuthMethod string            `json:"auth_method"`
	Cache      CacheInfo         `json:"device_cache"`
	Collectors []CollectorStatus `json:"collectors"`
	Devices    []DeviceRow       `json:"devices"`
	API        APIInfo           `json:"api"`
	// Failing is the count of this tailnet's collectors whose last run failed.
	Failing int `json:"failing"`
}

// APIInfo summarizes Tailscale API request health per endpoint. RateLimit is set
// only once a 429 has been observed.
type APIInfo struct {
	Endpoints []APIEndpoint `json:"endpoints"`
	RateLimit *APIRateLimit `json:"rate_limit,omitempty"`
	Auth      APIAuth       `json:"auth"`
}

// APIAuth summarizes how the client authenticates to the Tailscale API and the
// aggregate request/throttle totals across endpoints. It never carries a
// credential value. KeyExpiresAt/Warning are set only when known (API-key expiry
// is not parsed today, so they are usually empty).
type APIAuth struct {
	Method       string `json:"method"` // "oauth" | "apikey" | "workload_identity" | ""
	TotalCalls   int64  `json:"total_calls"`
	Total429     int64  `json:"total_429"`
	KeyExpiresAt string `json:"key_expires_at,omitempty"` // RFC3339 if known
	Warning      string `json:"warning,omitempty"`
}

// APIEndpoint is one low-cardinality endpoint's request aggregates. LastError is
// the transport error text only (never response data); LastAt/Last429At are
// RFC3339.
type APIEndpoint struct {
	Endpoint         string  `json:"endpoint"`
	Requests         int64   `json:"requests"`
	Errors           int64   `json:"errors"`
	Retries          int64   `json:"retries"`
	RateLimited      int64   `json:"rate_limited"`
	LastStatus       int     `json:"last_status"`
	LastError        string  `json:"last_error,omitempty"`
	LastAt           string  `json:"last_at,omitempty"`
	Last429At        string  `json:"last_429_at,omitempty"`
	DurationMsSeries []int64 `json:"duration_ms_series,omitempty"`
}

// APIRateLimit is the observed rate-limit signal: how many 429s have been seen
// and when the most recent one was.
type APIRateLimit struct {
	LastSeen string `json:"last_seen,omitempty"` // RFC3339
	Count    int64  `json:"count"`
}

// ServiceInfo is the identity/liveness header of the page.
type ServiceInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	Tailnet   string `json:"tailnet"`
	StartedAt string `json:"started_at"`
	UptimeSec int64  `json:"uptime_seconds"`
	Uptime    string `json:"uptime"`
	SelfObs   bool   `json:"self_observability"`
}

// TelemetryInfo describes the OTLP export target (never any credentials).
type TelemetryInfo struct {
	Protocol              string `json:"protocol"`
	Endpoint              string `json:"endpoint"`
	Insecure              bool   `json:"insecure"`
	MetricIntervalS       int64  `json:"metric_interval_seconds"`
	MetricExportBatchSize int    `json:"metric_export_batch_size"`
}

// CollectorStatus is one row of the collector status table.
type CollectorStatus struct {
	Name string `json:"name"`
	// Tailnet attributes the collector to its runtime, so the combined list and
	// health reasons disambiguate duplicate collector names in multi-tailnet mode
	// (#116). Empty in single-tailnet mode.
	Tailnet        string `json:"tailnet,omitempty"`
	IntervalSec    int64  `json:"interval_seconds"`
	Runs           int64  `json:"runs"`
	Failures       int64  `json:"failures"`
	HasRun         bool   `json:"has_run"`
	LastStartedAt  string `json:"last_started_at,omitempty"`
	LastFinishedAt string `json:"last_finished_at,omitempty"`
	LastDurationMs int64  `json:"last_duration_ms"`
	LastSuccess    bool   `json:"last_success"`
	LastError      string `json:"last_error,omitempty"`
	// ConsecutiveFailures is the current unbroken run of failures (0 on the last
	// success). SuccessRatePct is (runs-failures)/runs over the process lifetime.
	ConsecutiveFailures int64   `json:"consecutive_failures"`
	SuccessRatePct      float64 `json:"success_rate_pct"`
	// NextRunInSec is the time until the next scheduled tick (0 when due/overdue);
	// NextRunIn is the same as a human string. Overdue is set when the collector
	// has not run in over twice its interval (a wedged-tick signal).
	NextRunInSec int64  `json:"next_run_in_seconds"`
	NextRunIn    string `json:"next_run_in,omitempty"`
	Overdue      bool   `json:"overdue,omitempty"`
	// LastSuccessAt is the finish time of the most recent SUCCESSFUL run (empty if
	// never). FreshnessSec/Freshness are its age; FreshnessState buckets it into
	// "ok"|"warning"|"stale" (by multiples of the interval) or "none" when the
	// collector has never succeeded — the data-freshness column.
	LastSuccessAt  string `json:"last_success_at,omitempty"`
	FreshnessSec   int64  `json:"freshness_seconds"`
	Freshness      string `json:"freshness,omitempty"`
	FreshnessState string `json:"freshness_state,omitempty"`
	// DurationMsSeries and OutcomeSeries are the recent-run history (oldest first,
	// aligned) feeding the per-collector duration sparkline and outcome strip.
	DurationMsSeries []int64 `json:"duration_ms_series,omitempty"`
	OutcomeSeries    []bool  `json:"outcome_series,omitempty"`
	// Description is a one-line explanation of what this collector does, and
	// Metrics lists the signals it emits — both surfaced as the per-collector
	// info tooltip on the admin page. Sourced from the in-code metric catalog so
	// the tooltip can never drift from what is actually emitted.
	Description string        `json:"description,omitempty"`
	Metrics     []MetricBrief `json:"metrics,omitempty"`
	// Checkpoint is the window-collector checkpoint state (nil for snapshot
	// collectors, which keep no checkpoint).
	Checkpoint *CheckpointStatus `json:"checkpoint,omitempty"`
}

// MetricBrief is one signal a collector emits, shown in the collector's info
// tooltip (the OTEL source name plus its human description).
type MetricBrief struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CheckpointStatus is a window collector's persisted high-water-mark state. Lag
// is how far the high-water mark trails "now"; Stuck is set when that lag has
// grown well beyond the collector's interval (i.e. the window is not advancing).
type CheckpointStatus struct {
	HighWaterMark string `json:"high_water_mark,omitempty"` // RFC3339
	LagSec        int64  `json:"lag_seconds"`
	Lag           string `json:"lag"`
	Stuck         bool   `json:"stuck"`
}

// CacheInfo summarizes the device-enrichment cache.
type CacheInfo struct {
	Devices int    `json:"devices"`
	AgeSec  int64  `json:"age_seconds"`
	Age     string `json:"age"`
}

// RDNSInfo summarizes the reverse-DNS (PTR) enrichment cache. Enabled is false
// when enrichment.reverse_dns.enabled is off (the rest of the fields are then
// zero). HitRatePct is hits/(hits+misses+negatives) over the process lifetime.
type RDNSInfo struct {
	Enabled        bool    `json:"enabled"`
	Size           int     `json:"size"`
	Capacity       int     `json:"capacity"`
	TTL            string  `json:"ttl,omitempty"`
	NegativeTTL    string  `json:"negative_ttl,omitempty"`
	Hits           int64   `json:"hits"`
	Misses         int64   `json:"misses"`
	Negatives      int64   `json:"negatives"`
	QuerySuccess   int64   `json:"query_success"`
	QueryFail      int64   `json:"query_fail"`
	EvictedExpired int64   `json:"evicted_expired"`
	EvictedPurged  int64   `json:"evicted_purged"`
	Overflows      int64   `json:"overflows"`
	HitRatePct     float64 `json:"hit_rate_pct"`
	LastPurge      string  `json:"last_purge,omitempty"` // RFC3339
}

// FlowStoreInfo is the built-in flow view's store, combined across every
// observed tailnet (each keeps its own, but an operator cares about one number
// for the process). It exists on the status page so the memory the view costs,
// and any coverage it had to give up, are visible next to everything else —
// rather than only on /flows, which an operator with a problem may not open.
type FlowStoreInfo struct {
	Enabled bool `json:"enabled"`
	// Buckets currently held, of Capacity one-minute slots per tailnet.
	Buckets  int `json:"buckets"`
	Capacity int `json:"capacity"`
	// Observations recorded and Truncated folded into "__other__" by a cap.
	Observations int64 `json:"observations"`
	Truncated    int64 `json:"truncated"`
	// Retention is the configured window; Covered is what is actually held,
	// which is shorter until the ring fills. Empty when nothing is retained.
	Retention string `json:"retention"`
	Covered   string `json:"covered,omitempty"`
}

// DedupInfo is one cross-source de-duplication set's occupancy.
type DedupInfo struct {
	Name     string `json:"name"`
	Len      int    `json:"len"`
	Capacity int    `json:"capacity"`
}

// DeviceRow is one entry of the enrichment device table.
type DeviceRow struct {
	Name      string   `json:"name"`
	Hostname  string   `json:"hostname"`
	OS        string   `json:"os"`
	OSVersion string   `json:"os_version"`
	User      string   `json:"user"`
	Addrs     []string `json:"addrs"`
	Tags      []string `json:"tags,omitempty"`
	External  bool     `json:"external"`
}

// NodeDiscovery reports the node-metrics scraper's discovered/active targets.
type NodeDiscovery struct {
	Enabled       bool         `json:"enabled"`
	LastDiscovery string       `json:"last_discovery,omitempty"`
	LastOK        bool         `json:"last_ok"`
	Static        int          `json:"static"`
	Active        int          `json:"active"`
	Targets       []NodeTarget `json:"targets"`
}

// NodeTarget is one node-metrics scrape target.
type NodeTarget struct {
	Instance string `json:"instance"`
	URL      string `json:"url"`
	Source   string `json:"source"` // "static" | "discovered"
}

// CardinalityInfo surfaces the live active-series cardinality (self-obs only).
// Available is false when self-observability is off or no interval has been
// reported yet.
type CardinalityInfo struct {
	Available bool        `json:"available"`
	Total     int         `json:"total"`
	Series    []SeriesRow `json:"series,omitempty"`
	// TotalSeries is the recent trend of the total active-series count (oldest
	// first), feeding the cardinality sparkline. Populated only when self-obs is on.
	TotalSeries []int `json:"total_series,omitempty"`
	// Labels is the high-cardinality-label breakdown (distinct values per label
	// key, aggregated across metrics, sorted by total distinct desc).
	Labels []LabelRow `json:"labels,omitempty"`
	// Growth is the per-metric active-series growth over the retained history
	// window, sorted by absolute change desc.
	Growth []GrowthRow `json:"growth,omitempty"`
	// Thresholds are the configured warning/critical series-count levels.
	Thresholds CardinalityThresholds `json:"thresholds"`
	// Alerts lists metrics whose count crossed a threshold (critical first).
	Alerts []CardinalityAlert `json:"alerts,omitempty"`
	// TotalMetrics is the number of source metrics currently emitting series.
	TotalMetrics int `json:"total_metrics"`
}

// SeriesRow is the active-series count for one source metric. Level is set when
// the count crosses a configured threshold ("warning" | "critical"; "" = below).
type SeriesRow struct {
	Metric   string `json:"metric"`
	PromName string `json:"prom_name"`
	Count    int    `json:"count"`
	Capped   bool   `json:"capped"`
	Level    string `json:"level,omitempty"`
}

// LabelRow is one label key's distinct-value cardinality aggregated across every
// metric that carries it. Metrics is the per-metric breakdown (with example
// values). Capped is true when any contributing (metric,label) hit the cap.
type LabelRow struct {
	Label         string           `json:"label"`
	TotalDistinct int              `json:"total_distinct"`
	Capped        bool             `json:"capped"`
	Metrics       []LabelMetricRow `json:"metrics"`
}

// LabelMetricRow is one metric's contribution to a label's cardinality.
type LabelMetricRow struct {
	Metric   string   `json:"metric"`
	PromName string   `json:"prom_name"`
	Distinct int      `json:"distinct"`
	Capped   bool     `json:"capped"`
	Examples []string `json:"examples,omitempty"`
}

// GrowthRow is one metric's active-series change over the retained history
// window. DeltaPct is the percentage change from the oldest retained sample to
// the current count (0 when the window has < 2 samples or started at 0).
type GrowthRow struct {
	Metric   string  `json:"metric"`
	PromName string  `json:"prom_name"`
	Current  int     `json:"current"`
	DeltaPct float64 `json:"delta_pct"`
	Series   []int   `json:"series,omitempty"`
}

// CardinalityThresholds are the configured warning/critical series-count levels
// (0 = that level disabled).
type CardinalityThresholds struct {
	Warning  int `json:"warning"`
	Critical int `json:"critical"`
}

// CardinalityAlert is one metric flagged for crossing a threshold.
type CardinalityAlert struct {
	Metric string `json:"metric"`
	Count  int    `json:"count"`
	Level  string `json:"level"` // "critical" | "warning"
}

// ReceiversInfo reports which optional ingestion receivers are enabled.
//
// Kept as-is for existing consumers; Status.Components is the richer view and
// covers the listeners too.
type ReceiversInfo struct {
	Streaming bool `json:"streaming_enabled"`
	Webhook   bool `json:"webhook_enabled"`
}

// DeliverySignal is one OTLP signal's delivery state: metrics, logs or traces.
//
// It answers "is data reaching the backend", which the page previously guessed
// at from collector success — so it could show a recent "last export" while
// every OTLP request was failing (#317). Emitted and delivered are kept
// separate on purpose: ThroughputInfo counts what was handed to an exporter,
// this counts what came back.
type DeliverySignal struct {
	Signal   string `json:"signal"`
	Exports  int64  `json:"exports"`
	Failures int64  `json:"failures"`
	// ConsecutiveFailures is the current streak; Failing marks it sustained
	// rather than a blip, and is what drags overall health to degraded.
	ConsecutiveFailures int64  `json:"consecutive_failures"`
	Failing             bool   `json:"failing"`
	LastSuccessAt       string `json:"last_success_at,omitempty"` // RFC3339
	LastFailureAt       string `json:"last_failure_at,omitempty"` // RFC3339
	LastDurationMs      int64  `json:"last_duration_ms"`
	// LastErrorClass is a value from a CLOSED set (timeout, unauthenticated,
	// rate_limited, unavailable, invalid, canceled, other) — never the backend's
	// response text, which is where an echoed credential would be.
	LastErrorClass string `json:"last_error_class,omitempty"`
}

// ComponentStatus is one long-running non-collector subsystem: the admin and
// Prometheus listeners, the stream and webhook receivers, and the ingress WAL.
//
// It exists so a failure is VISIBLE rather than merely reflected in the overall
// verdict (#318). Before it, the page reported receivers as two bare enabled
// booleans and said nothing at all about the listeners, so "degraded" left an
// operator to guess which subsystem had gone.
//
// Enabled and Failed are independent on purpose: a disabled component is still
// listed, because "off" and "missing from the page" are very different answers
// to "why am I not getting webhook events".
type ComponentStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Failed  bool   `json:"failed"`
	// Reason is the underlying failure text, empty unless Failed. It is the same
	// string /readyz returns.
	Reason string `json:"reason,omitempty"`
}

// ProfilingInfo reports the continuous-profiling configuration (no credentials).
type ProfilingInfo struct {
	PprofEnabled     bool   `json:"pprof_enabled"`
	PyroscopeEnabled bool   `json:"pyroscope_enabled"`
	PyroscopeServer  string `json:"pyroscope_server,omitempty"`
}

// RuntimeInfo is a point-in-time Go runtime snapshot plus short-term trend
// series (oldest first) feeding the runtime sparklines.
type RuntimeInfo struct {
	Goroutines       int       `json:"goroutines"`
	HeapAllocB       uint64    `json:"heap_alloc_bytes"`
	HeapAlloc        string    `json:"heap_alloc"`
	NumGC            uint32    `json:"num_gc"`
	GOMAXPROCS       int       `json:"gomaxprocs"`
	GoroutinesSeries []int     `json:"goroutines_series,omitempty"`
	HeapAllocSeries  []uint64  `json:"heap_alloc_series,omitempty"`
	GCRateSeries     []float64 `json:"gc_rate_series,omitempty"`
}

// ThroughputInfo is the volume of telemetry produced at the EMIT boundary — what
// the collectors handed to the emitter, before batching or export. MetricPoints
// and LogRecords are cumulative since process start; the *PerSec values are the
// most recent sampler interval's rate (0 until two samples exist), and the
// series are the retained rate trend (oldest first) feeding the throughput chart.
type ThroughputInfo struct {
	MetricPoints uint64 `json:"metric_points"`
	LogRecords   uint64 `json:"log_records"`

	MetricPointsPerSec float64 `json:"metric_points_per_sec"`
	LogRecordsPerSec   float64 `json:"log_records_per_sec"`

	MetricPointsPerSecSeries []float64 `json:"metric_points_per_sec_series"`
	LogRecordsPerSecSeries   []float64 `json:"log_records_per_sec_series"`
}

// FleetInfo is the collector-fleet aggregate across every tailnet runtime.
// Active/Failing/MeanDurationMs are live at snapshot time (collectors that have
// never run are excluded from all three); the series are the retained trend
// (oldest first) feeding the fleet charts.
type FleetInfo struct {
	Active         int     `json:"active"`
	Failing        int     `json:"failing"`
	MeanDurationMs float64 `json:"mean_duration_ms"`

	FailingSeries        []int     `json:"failing_series"`
	MeanDurationMsSeries []float64 `json:"mean_duration_ms_series"`
}

// MetricRow is one entry of the emitted-metrics catalog table. Series is the
// live active-series count when self-observability is on (0 = unknown).
type MetricRow struct {
	Name        string   `json:"name"`
	PromName    string   `json:"prom_name"`
	Instrument  string   `json:"instrument"`
	Unit        string   `json:"unit"`
	Group       string   `json:"group"`
	Description string   `json:"description"`
	Attributes  []string `json:"attributes,omitempty"`
	Series      int      `json:"series,omitempty"`
}

// LogRow is one entry of the emitted-log-events catalog table.
type LogRow struct {
	Name        string   `json:"name"`
	Severity    string   `json:"severity"`
	Group       string   `json:"group"`
	Description string   `json:"description"`
	Attributes  []string `json:"attributes,omitempty"`
}

// Scope preflight outcomes (#425). A CLOSED set, safe as a label or a UI badge
// class. The preflight compares the REQUESTED OAuth scopes from configuration
// against each collector's required scope — nothing here reflects what the
// server actually granted, so it is advisory only and the runtime 403
// classification in State remains authoritative.
const (
	// ScopeSatisfied means the configured scopes cover the requirement.
	ScopeSatisfied = "satisfied"
	// ScopeInsufficient means they demonstrably do not. Advisory: a modeling
	// error here must never stop collection, only warn.
	ScopeInsufficient = "insufficient"
	// ScopeUnknown means the answer is not knowable: either the credential is not
	// an OAuth client (an API key carries no scope list), or upstream does not
	// document a scope for this capability. Never warned about.
	ScopeUnknown = "unknown"
	// ScopeNotApplicable means the capability needs no Tailscale API scope at all
	// (node-local scrapes, object-store ingestion). Distinct from ScopeUnknown so
	// "we deliberately require nothing" cannot be confused with "we do not know".
	ScopeNotApplicable = "not_applicable"
)

// Reasons a capability is not currently active. A CLOSED set.
const (
	// CapabilityReasonUnsupported means the active control plane does not expose
	// the API at all (e.g. DNS under Headscale). Checked first: it holds
	// regardless of what the configuration asks for.
	CapabilityReasonUnsupported = "provider_unsupported"
	// CapabilityReasonConfigDisabled means the operator turned it off.
	CapabilityReasonConfigDisabled = "config_disabled"
	// CapabilityReasonNotRegistered means it is enabled and supported yet no
	// collector was registered — the ingestion source excludes polling, or a
	// required sub-configuration (e.g. node-metrics targets) is absent.
	CapabilityReasonNotRegistered = "not_registered"
)

// CapabilityRow is one row of the configuration-to-capability matrix: an enabled
// collector, or one of its optional per-entity subrequests (Subrequest set).
//
// Collector/Capability/Active/Reason describe the CONFIGURED intent and what the
// composition root actually did with it; ScopeStatus is the advisory startup
// preflight; State/LastProbe are the live, server-authoritative truth.
type CapabilityRow struct {
	Collector string `json:"collector"`
	// Subrequest is empty on a collector row and names the bounded per-entity
	// subrequest type (e.g. "device_invites") on a subrequest row.
	Subrequest string `json:"subrequest,omitempty"`
	// Capability is the provider feature key this row needs, and the value of the
	// tailscale.capability attribute on the scope-preflight metric. Unique across
	// rows, so a subrequest row carries its own capability key.
	Capability string `json:"capability"`
	// RequiredScopes are the OAuth scopes upstream documents for this capability.
	// Empty when none are needed or none are modeled — see ScopeStatus.
	RequiredScopes []string `json:"required_scopes,omitempty"`
	// MissingScopes lists the required scopes the configured set does not cover.
	// Populated only when ScopeStatus is ScopeInsufficient.
	MissingScopes     []string `json:"missing_scopes,omitempty"`
	ConfigEnabled     bool     `json:"config_enabled"`
	ProviderSupported bool     `json:"provider_supported"`
	// Active means a collector is actually registered and running for this row.
	Active bool `json:"active"`
	// Reason explains a non-Active row (one of the CapabilityReason* constants);
	// empty when Active.
	Reason string `json:"reason,omitempty"`
	// ScopeStatus is one of the Scope* constants.
	ScopeStatus string `json:"scope_status"`
	// State is the aggregated apistate.State across every probed operation of this
	// collector: the most operator-relevant one, so a partial scope denial is never
	// masked by a sibling success. "unknown" until something has been probed.
	State string `json:"state"`
	// Actionable mirrors apistate.State.Actionable() — false for the expected
	// "disabled"/"unknown" states, so a feature that is simply off never pages.
	Actionable bool `json:"actionable"`
	// LastProbe is the most recent probe time across this row's operations
	// (RFC3339; empty when never probed).
	LastProbe string `json:"last_probe,omitempty"`
	// Source is the effective ingestion source for the log collectors
	// ("poll"/"stream"/"both"/object-store); empty for snapshot collectors.
	Source string `json:"source,omitempty"`
	// Operations is the per-operation breakdown behind State, on collector rows only.
	Operations []CapabilityOperation `json:"operations,omitempty"`
}

// CapabilityOperation is one probed API operation's latest availability.
type CapabilityOperation struct {
	Operation string `json:"operation"`
	State     string `json:"state"`
	LastProbe string `json:"last_probe,omitempty"`
}

// ConfigSummary is the redacted configuration overview. Secret VALUES never
// appear here — only "<thing>Set" booleans and header KEY names.
type ConfigSummary struct {
	LogLevel        string `json:"log_level"`
	AuthMethod      string `json:"auth_method"`
	CheckpointStore string `json:"checkpoint_store"`
	// CheckpointPath is the file ACTUALLY in use (empty for the memory store) and
	// CheckpointReason explains any divergence from the configured values — an
	// unwritable path, a relocation to the platform state directory (#336), or a
	// corrupt file renamed aside. Both are effective values, not config values,
	// so an operator can see where state went without reading startup logs.
	CheckpointPath    string   `json:"checkpoint_path,omitempty"`
	CheckpointReason  string   `json:"checkpoint_reason,omitempty"`
	EnabledCollectors []string `json:"enabled_collectors"`
	APIKeySet         bool     `json:"api_key_set"`
	OAuthSecretSet    bool     `json:"oauth_secret_set"`
	GCloudTokenSet    bool     `json:"grafana_cloud_token_set"`
	StreamTokenSet    bool     `json:"stream_token_set"`
	WebhookSecretSet  bool     `json:"webhook_secret_set"`
	PyroscopeAuthSet  bool     `json:"pyroscope_auth_set"`
	OTLPHeaderKeys    []string `json:"otlp_header_keys,omitempty"`
}
