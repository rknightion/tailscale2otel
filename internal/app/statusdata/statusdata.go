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
	// Health is the at-a-glance verdict: "healthy", "degraded" or "starting".
	// HealthReasons explains a non-healthy verdict (empty when healthy).
	Health        string            `json:"health"`
	HealthReasons []string          `json:"health_reasons,omitempty"`
	Telemetry     TelemetryInfo     `json:"telemetry"`
	Collectors    []CollectorStatus `json:"collectors"`
	Cache         CacheInfo         `json:"device_cache"`
	RDNS          RDNSInfo          `json:"reverse_dns"`
	Dedup         []DedupInfo       `json:"dedup_sets"`
	Devices       []DeviceRow       `json:"devices"`
	NodeDiscovery NodeDiscovery     `json:"node_discovery"`
	Cardinality   CardinalityInfo   `json:"cardinality"`
	Receivers     ReceiversInfo     `json:"receivers"`
	Profiling     ProfilingInfo     `json:"profiling"`
	Runtime       RuntimeInfo       `json:"runtime"`
	API           APIInfo           `json:"api"`
	Metrics       []MetricRow       `json:"metrics"`
	LogEvents     []LogRow          `json:"log_events"`
	Config        ConfigSummary     `json:"config"`
	GeneratedAt   string            `json:"generated_at"`
}

// APIInfo summarizes Tailscale API request health per endpoint. RateLimit is set
// only once a 429 has been observed.
type APIInfo struct {
	Endpoints []APIEndpoint `json:"endpoints"`
	RateLimit *APIRateLimit `json:"rate_limit,omitempty"`
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
	Protocol        string `json:"protocol"`
	Endpoint        string `json:"endpoint"`
	Insecure        bool   `json:"insecure"`
	MetricIntervalS int64  `json:"metric_interval_seconds"`
}

// CollectorStatus is one row of the collector status table.
type CollectorStatus struct {
	Name           string `json:"name"`
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
}

// SeriesRow is the active-series count for one source metric.
type SeriesRow struct {
	Metric   string `json:"metric"`
	PromName string `json:"prom_name"`
	Count    int    `json:"count"`
	Capped   bool   `json:"capped"`
}

// ReceiversInfo reports which optional ingestion receivers are enabled.
type ReceiversInfo struct {
	Streaming bool `json:"streaming_enabled"`
	Webhook   bool `json:"webhook_enabled"`
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

// ConfigSummary is the redacted configuration overview. Secret VALUES never
// appear here — only "<thing>Set" booleans and header KEY names.
type ConfigSummary struct {
	LogLevel          string   `json:"log_level"`
	AuthMethod        string   `json:"auth_method"`
	CheckpointStore   string   `json:"checkpoint_store"`
	EnabledCollectors []string `json:"enabled_collectors"`
	APIKeySet         bool     `json:"api_key_set"`
	OAuthSecretSet    bool     `json:"oauth_secret_set"`
	GCloudTokenSet    bool     `json:"grafana_cloud_token_set"`
	StreamTokenSet    bool     `json:"stream_token_set"`
	WebhookSecretSet  bool     `json:"webhook_secret_set"`
	PyroscopeAuthSet  bool     `json:"pyroscope_auth_set"`
	OTLPHeaderKeys    []string `json:"otlp_header_keys,omitempty"`
}
