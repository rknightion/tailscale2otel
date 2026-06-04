// Package statusdata defines the data model rendered by the admin status page
// (internal/app/statushtml) and served verbatim as JSON at /api/status.json.
// Keeping it a dependency-free leaf lets the HTML and JSON paths share a single
// source of truth so the two can never drift. All fields are plain values;
// durations are pre-rendered as both a numeric (seconds/ms) and a human string,
// and times as RFC3339 strings, so neither consumer needs formatting logic.
package statusdata

// Status is the full admin status snapshot.
type Status struct {
	Service       ServiceInfo       `json:"service"`
	Telemetry     TelemetryInfo     `json:"telemetry"`
	Collectors    []CollectorStatus `json:"collectors"`
	Cache         CacheInfo         `json:"device_cache"`
	Dedup         []DedupInfo       `json:"dedup_sets"`
	Devices       []DeviceRow       `json:"devices"`
	NodeDiscovery NodeDiscovery     `json:"node_discovery"`
	Cardinality   CardinalityInfo   `json:"cardinality"`
	Receivers     ReceiversInfo     `json:"receivers"`
	Profiling     ProfilingInfo     `json:"profiling"`
	Runtime       RuntimeInfo       `json:"runtime"`
	Metrics       []MetricRow       `json:"metrics"`
	LogEvents     []LogRow          `json:"log_events"`
	Config        ConfigSummary     `json:"config"`
	GeneratedAt   string            `json:"generated_at"`
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
}

// CacheInfo summarizes the device-enrichment cache.
type CacheInfo struct {
	Devices int    `json:"devices"`
	AgeSec  int64  `json:"age_seconds"`
	Age     string `json:"age"`
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

// RuntimeInfo is a point-in-time Go runtime snapshot.
type RuntimeInfo struct {
	Goroutines int    `json:"goroutines"`
	HeapAllocB uint64 `json:"heap_alloc_bytes"`
	HeapAlloc  string `json:"heap_alloc"`
	NumGC      uint32 `json:"num_gc"`
	GOMAXPROCS int    `json:"gomaxprocs"`
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
