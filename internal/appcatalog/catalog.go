// Package appcatalog holds the app layer's self-observability metric
// descriptors (the heartbeat up gauge and the Tailscale API request/retry
// counters) as the SINGLE SOURCE OF TRUTH for both their emission and their
// documentation.
//
// It lives in its own leaf package — rather than in internal/app — so that
// internal/catalog (the docs aggregator) can pull these descriptors WITHOUT
// importing internal/app. That keeps internal/app free to import internal/catalog
// itself (the admin status page renders the full catalog), which a direct
// catalog->app dependency would forbid.
//
// The app-layer emit sites (internal/app/heartbeat.go, internal/app/selfobs.go)
// reference these descriptors so the emitted unit/description cannot drift from
// what is documented; internal/app/catalog_test.go is the drift guard. These
// share the cross-cutting "Self-observability" doc section with the telemetry
// build/export/cardinality metrics, the collector scrape.* metrics, and the
// devices enrich.cache_* metrics.
//
// The TLS-certificate-reload emit helpers (EmitTLSCertLoaded,
// EmitTLSCertReloadFailure, #316) are the one place this package also exports
// EMISSION, not just declaration: internal/app's admin/Prometheus listeners
// and internal/stream's streaming receiver all need to record the same three
// metrics identically, but internal/stream cannot import internal/app (an
// import cycle — internal/app already imports internal/stream to wire the
// receiver). This leaf package is reachable from both without creating one,
// so it is the single place the emission itself — not just its shape — is
// shared rather than duplicated.
package appcatalog

import (
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// GroupSelfObs is the docs section these metrics render under.
const GroupSelfObs = "Self-observability"

// Self-observability metric names emitted from the app layer (the scheduler and
// collectors emit the rest; see internal/collector and internal/telemetry).
const (
	// MetricUp is the heartbeat liveness gauge name.
	MetricUp = "tailscale2otel.up"
	// MetricAPIRequests counts Tailscale API requests.
	MetricAPIRequests = "tailscale2otel.api.requests"
	// MetricAPIRetries counts Tailscale API retry attempts.
	MetricAPIRetries = "tailscale2otel.api.retries"
	// MetricAPIDuration is the API request wall-clock latency histogram name.
	MetricAPIDuration = "tailscale2otel.api.duration"
	// MetricAPIRateLimitWait is the histogram of time API requests spent blocked
	// on the client-side rate limiter.
	MetricAPIRateLimitWait = "tailscale2otel.api.rate_limit.wait"
	// MetricUpdateAvailable is the self update-available flag name (C4).
	MetricUpdateAvailable = "tailscale2otel.update_available"
)

// Go runtime self-observability metric names. These expose the exporter's own
// heap/GC/goroutine health (read from runtime.MemStats) so a goroutine leak,
// heap growth, or GC pressure is alertable — none of which is otherwise visible
// in the OTLP pipeline (the admin status page reads the same source, but does
// not export it).
const (
	MetricRuntimeGoroutines  = "tailscale2otel.runtime.goroutines"
	MetricRuntimeGomaxprocs  = "tailscale2otel.runtime.gomaxprocs"
	MetricRuntimeHeapAlloc   = "tailscale2otel.runtime.memory.heap_alloc"
	MetricRuntimeHeapSys     = "tailscale2otel.runtime.memory.heap_sys"
	MetricRuntimeHeapInuse   = "tailscale2otel.runtime.memory.heap_inuse"
	MetricRuntimeStackInuse  = "tailscale2otel.runtime.memory.stack_inuse"
	MetricRuntimeMemSys      = "tailscale2otel.runtime.memory.sys"
	MetricRuntimeHeapObjects = "tailscale2otel.runtime.memory.heap_objects"
	// MetricRuntimeAllocBytes is named ".memory.alloc" (not ".alloc_bytes") so the
	// "By" unit's `_bytes` suffix is not doubled under Prometheus normalization
	// (-> tailscale2otel_runtime_memory_alloc_bytes_total), matching the Go
	// client's go_memstats_alloc_bytes_total convention.
	MetricRuntimeAllocBytes    = "tailscale2otel.runtime.memory.alloc"
	MetricRuntimeGCNextTarget  = "tailscale2otel.runtime.gc.next_target"
	MetricRuntimeGCCPUFraction = "tailscale2otel.runtime.gc.cpu_fraction"
	MetricRuntimeGCCount       = "tailscale2otel.runtime.gc.count"
	MetricRuntimeGCPauseTime   = "tailscale2otel.runtime.gc.pause_time"
)

// MetricComponentErrors counts failures of non-collector subsystems (the
// streaming/webhook receivers, the admin server, and streaming auto-configure)
// that are otherwise only logged. Keyed by a CLOSED set of component values so
// cardinality stays bounded.
const MetricComponentErrors = "tailscale2otel.component.errors"

// Component values for MetricComponentErrors (the semconv.AttrComponent attr).
// Also reused, unchanged, as the component label for the TLS-reload metrics
// below (admin/metrics/stream are exactly the three TLS-capable listeners).
const (
	ComponentStream        = "stream"
	ComponentWebhook       = "webhook"
	ComponentAdmin         = "admin"
	ComponentAutoConfigure = "auto_configure"
	ComponentMetrics       = "metrics"
	ComponentIngressWAL    = "ingress_wal"
)

// TLS-certificate-reload metric names (#316). Each TLS-enabled listener
// (admin, metrics/Prometheus, stream) reports its active certificate's
// validity window and reload health, keyed by semconv.AttrComponent so the
// three listeners share one set of series rather than each minting its own
// metric name.
const (
	// MetricTLSCertNotBefore is the active certificate's notBefore gauge name.
	MetricTLSCertNotBefore = "tailscale2otel.tls.cert.not_before"
	// MetricTLSCertNotAfter is the active certificate's notAfter (expiry) gauge name.
	MetricTLSCertNotAfter = "tailscale2otel.tls.cert.not_after"
	// MetricTLSCertReloadedAt is the most recent successful reload's timestamp gauge name.
	MetricTLSCertReloadedAt = "tailscale2otel.tls.cert.reload.last_success"
	// MetricTLSCertReloadFailures counts reload attempts that did not produce a
	// valid keypair (the listener keeps serving the previous certificate).
	MetricTLSCertReloadFailures = "tailscale2otel.tls.cert.reload.failures"
)

// MetricAdminAuthRejected counts admin HTTP requests rejected by the admin auth
// gate (the status page and pprof handlers), keyed by a CLOSED set of reasons so
// cardinality stays bounded. The /healthz and /readyz probes are never gated and
// never counted here.
const MetricAdminAuthRejected = "tailscale2otel.admin.auth.rejected"

// De-duplication self-observability metric names. The cross-source dedup sets
// (poll vs stream, webhook vs audit) bound memory by evicting the oldest keys;
// these expose their fill level and eviction pressure so an undersized set is
// visible rather than silently dropping keys.
const (
	MetricDedupSize      = "tailscale2otel.dedup.size"
	MetricDedupEvictions = "tailscale2otel.dedup.evictions"
	// MetricDedupHits counts duplicate keys suppressed by a de-duplication set
	// (the proof the set is doing work; size+evictions alone can't show hits).
	MetricDedupHits = "tailscale2otel.dedup.hits"
)

// Process self-observability metric names. OTel-standard process.* names (NOT the
// tailscale2otel.* namespace) so generic OTel/host dashboards light up without
// bespoke knowledge of our runtime.* metrics. Sourced from the Go stdlib only
// (uptime from the start time; cpu.time from syscall.Getrusage on unix).
const (
	MetricProcessUptime  = "process.uptime"
	MetricProcessCPUTime = "process.cpu.time"
)

// Config-health self-observability metric names. Surface config.Warnings()/
// Validate() at runtime (today warnings are log-only at startup) so misconfig is
// visible/alertable rather than buried in boot logs.
const (
	MetricConfigWarnings = "tailscale2otel.config.warnings"
	MetricConfigValid    = "tailscale2otel.config.valid"
)

// Ingress-WAL capacity metrics are process-global and deliberately carry no
// route, tailnet, filesystem, or error attributes.
const (
	MetricIngressWALPendingEntries    = "tailscale2otel.ingress_wal.pending.entries"
	MetricIngressWALPendingSize       = "tailscale2otel.ingress_wal.pending.size"
	MetricIngressWALOrphanStages      = "tailscale2otel.ingress_wal.orphan.stages"
	MetricIngressWALOrphanSize        = "tailscale2otel.ingress_wal.orphan.size"
	MetricIngressWALCompletionMarkers = "tailscale2otel.ingress_wal.completion.markers"
)

// Ingestion-volume self-observability metric names. Emitted (via an app-built
// closure) at each ingestion-path boundary so an operator can attribute log/DPM
// cost to poll vs stream vs webhook and to flow vs audit. Note the per-path
// receivers also emit domain counters (tailscale.stream.records,
// tailscale.webhook.events); these tailscale2otel.* counters are the unified,
// cross-path cost view.
const (
	MetricIngestRecords = "tailscale2otel.ingest.records"
	// MetricIngestBytes is named ".ingest.size" (not ".ingest.bytes") so the "By"
	// unit's `_bytes` suffix is not doubled under Prometheus normalization
	// (-> tailscale2otel_ingest_size_bytes_total), the same naming guard as
	// MetricRuntimeAllocBytes.
	MetricIngestBytes = "tailscale2otel.ingest.size"
	// MetricIngestEventAge measures accepted-at minus event time.
	MetricIngestEventAge = "tailscale2otel.ingest.event.age"
	// MetricIngestCaptureDelay measures capture/observation time minus event time.
	MetricIngestCaptureDelay = "tailscale2otel.ingest.capture.delay"
	// MetricIngestLastEventTimestamp is the greatest accepted event timestamp.
	MetricIngestLastEventTimestamp = "tailscale2otel.ingest.last_event_timestamp"
	// MetricIngestTimestampSkew counts inverted timestamp relationships.
	MetricIngestTimestampSkew = "tailscale2otel.ingest.timestamp_skew"
)

// MetricSeriesByGroup is the per-catalog-group active-series rollup: the sum of
// tailscale2otel.series.active over every metric in a docs group, keyed by group.
// A name-keyed view (series.active) summed by the area that owns each metric.
const MetricSeriesByGroup = "tailscale2otel.series.by_group"

// MetricPIIFilterCategory is the PII-redaction-state gauge name.
const MetricPIIFilterCategory = "tailscale2otel.pii_filter.category"

// Prometheus pull-endpoint (/metrics) serving metrics (#377). The pull path used
// to be entirely unobservable from the outside: a scrape that was rejected,
// throttled, timed out, or that hit a Gather collision left nothing behind but a
// log line (or, for a Gather collision under ContinueOnError, a silently
// truncated response with an HTTP 200). These are the exporter watching its own
// pull endpoint, so an operator can tell "the scraper is not asking" apart from
// "the endpoint is failing to answer".
//
// Every attribute set here is CLOSED: `outcome` is one of three classified
// values and `reason` is one of three rejection causes, so no amount of scrape
// traffic can grow the series count.
const (
	// MetricMetricsScrapeRequests counts /metrics requests that passed the auth
	// gate, by classified outcome.
	MetricMetricsScrapeRequests = "tailscale2otel.metrics.scrape.requests"
	// MetricMetricsScrapeInFlight is the number of /metrics requests currently
	// being served (an UpDownCounter, so it comes back down).
	MetricMetricsScrapeInFlight = "tailscale2otel.metrics.scrape.in_flight"
	// MetricMetricsScrapeDuration is the /metrics serving-latency histogram.
	MetricMetricsScrapeDuration = "tailscale2otel.metrics.scrape.duration"
	// MetricMetricsScrapeGatherErrors counts Gather errors encountered while
	// serving /metrics. Under ContinueOnError these do NOT fail the request, so
	// nothing else makes them visible.
	MetricMetricsScrapeGatherErrors = "tailscale2otel.metrics.scrape.gather_errors"
	// MetricMetricsAuthRejected counts /metrics requests refused by the auth
	// gate, by a closed set of reasons. It replaces a per-request WARN.
	MetricMetricsAuthRejected = "tailscale2otel.metrics.auth.rejected"
)

// Prometheus pull-endpoint serving descriptors (#377).
var (
	DocMetricsScrapeRequests = metricdoc.Metric{
		Name:       MetricMetricsScrapeRequests,
		Unit:       semconv.UnitRequests,
		Instrument: metricdoc.Counter,
		Description: "Prometheus `/metrics` scrapes served after passing the auth gate, by classified " +
			"`outcome`: `success` (2xx), `unavailable` (503 — the request was shed by " +
			"`prometheus.max_requests_in_flight` or exceeded `prometheus.timeout`), or `error` (any other " +
			"status). Rejected requests are NOT counted here; they are counted by " +
			"tailscale2otel.metrics.auth.rejected. A flat `success` rate is how you tell a scraper that " +
			"stopped asking apart from an endpoint that stopped answering.",
		Attributes: []string{"outcome"},
		Group:      GroupSelfObs,
	}
	DocMetricsScrapeInFlight = metricdoc.Metric{
		Name:       MetricMetricsScrapeInFlight,
		Unit:       semconv.UnitRequests,
		Instrument: metricdoc.UpDownCounter,
		Description: "Prometheus `/metrics` requests currently being served. A value pinned at " +
			"`prometheus.max_requests_in_flight` means scrapes are being shed; a value that climbs and " +
			"never returns to zero means collection is hanging rather than slow.",
		Group: GroupSelfObs,
	}
	DocMetricsScrapeDuration = metricdoc.Metric{
		Name:       MetricMetricsScrapeDuration,
		Unit:       semconv.UnitSeconds,
		Instrument: metricdoc.Histogram,
		Description: "Wall-clock seconds spent serving one Prometheus `/metrics` request, by the same " +
			"classified `outcome` as tailscale2otel.metrics.scrape.requests. Covers gather plus encode plus " +
			"write, so it is what the scraper's own timeout is racing against.",
		Attributes: []string{"outcome"},
		Group:      GroupSelfObs,
	}
	DocMetricsScrapeGatherErrors = metricdoc.Metric{
		Name:       MetricMetricsScrapeGatherErrors,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Counter,
		Description: "Prometheus registry Gather errors hit while serving `/metrics`. The handler runs with " +
			"`ContinueOnError` (#103), so these return HTTP 200 with the *remaining* series and are " +
			"otherwise invisible — a non-zero rate means some series are being dropped from every scrape, " +
			"most commonly a duplicate-series collision after `pii_filter.tailnet_name` removed the " +
			"distinguishing label.",
		Group: GroupSelfObs,
	}
	DocMetricsAuthRejected = metricdoc.Metric{
		Name:       MetricMetricsAuthRejected,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Counter,
		Description: "Prometheus `/metrics` requests refused by the auth gate, by `reason`: " +
			"`missing_credentials`, `bad_credentials`, or `auth_required` (no `prometheus.auth.token` on a " +
			"network-reachable bind — misconfiguration rather than a bad caller). Counted per request; the " +
			"`auth_required` misconfiguration is logged once per process rather than once per scrape, so " +
			"this counter is the only per-request signal.",
		Attributes: []string{"reason"},
		Group:      GroupSelfObs,
	}
)

// Descriptors for the app layer's self-observability metrics. Exported so the
// emit sites in package app can reference them.
var (
	DocUp = metricdoc.Metric{
		Name:        MetricUp,
		Unit:        "1",
		Instrument:  metricdoc.Gauge,
		Description: "Liveness flag: `1` while the service is running and reporting.",
		Group:       GroupSelfObs,
	}
	DocAPIRequests = metricdoc.Metric{
		Name:        MetricAPIRequests,
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "Tailscale API requests, by endpoint and HTTP status code.",
		Attributes:  []string{"endpoint", "http.response.status_code"},
		Group:       GroupSelfObs,
	}
	DocAPIRetries = metricdoc.Metric{
		Name:        MetricAPIRetries,
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "API retry attempts, by endpoint.",
		Attributes:  []string{"endpoint"},
		Group:       GroupSelfObs,
	}
	DocAPIDuration = metricdoc.Metric{
		Name:        MetricAPIDuration,
		Unit:        "s",
		Instrument:  metricdoc.Histogram,
		Description: "Tailscale API request wall-clock latency in seconds, by endpoint and HTTP status code. Covers the full logical request including any retry backoff (not just server time). Use the 429 status-code bucket here plus tailscale2otel.api.retries for rate-limit visibility — the Tailscale API exposes no rate-limit-remaining headers. When tracing is enabled, datapoints carry trace exemplars linking to the API request span.",
		Attributes:  []string{"endpoint", "http.response.status_code"},
		Group:       GroupSelfObs,
	}
	DocAPIRateLimitWait = metricdoc.Metric{
		Name:        MetricAPIRateLimitWait,
		Unit:        "s",
		Instrument:  metricdoc.Histogram,
		Description: "Time in seconds a Tailscale API request spent blocked on the client-side rate limiter (`tailscale.http.rate_limit`) before its first attempt, by endpoint. Recorded separately from and excluded from tailscale2otel.api.duration so latency reflects genuine API/network + backoff time. A rising distribution here means the configured rate limit is throttling the poller — raise `rate_limit` or lengthen collector intervals. Only requests that actually waited are recorded (a 0-wait request is skipped).",
		Attributes:  []string{"endpoint"},
		Group:       GroupSelfObs,
	}
	DocUpdateAvailable = metricdoc.Metric{
		Name:        MetricUpdateAvailable,
		Unit:        "1",
		Instrument:  metricdoc.Gauge,
		Description: "`1` when a newer tailscale2otel release is available on GitHub than the running build, else `0` (a **flag**, despite the `_ratio` Prometheus suffix). Emitted only when `version_checks.self` is enabled and both the running and latest versions parse — dev builds (version `dev`) never emit. Fail-open: a blocked/failed GitHub fetch emits nothing.",
		Group:       GroupSelfObs,
	}
)

var (
	DocIngressWALPendingEntries = metricdoc.Metric{
		Name:        MetricIngressWALPendingEntries,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Durable ingress-WAL entries awaiting successful processor application and metric/log flush (a **count**, despite the `_ratio` Prometheus suffix).",
		Group:       GroupSelfObs,
	}
	DocIngressWALPendingSize = metricdoc.Metric{
		Name:        MetricIngressWALPendingSize,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Encoded on-disk bytes consumed by durable ingress-WAL entries.",
		Group:       GroupSelfObs,
	}
	DocIngressWALOrphanStages = metricdoc.Metric{
		Name:        MetricIngressWALOrphanStages,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Ingress-WAL staging files retained for bounded recovery cleanup (a **count**, despite the `_ratio` Prometheus suffix).",
		Group:       GroupSelfObs,
	}
	DocIngressWALOrphanSize = metricdoc.Metric{
		Name:        MetricIngressWALOrphanSize,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Encoded on-disk bytes consumed by retained ingress-WAL staging files.",
		Group:       GroupSelfObs,
	}
	DocIngressWALCompletionMarkers = metricdoc.Metric{
		Name:        MetricIngressWALCompletionMarkers,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Transient ingress-WAL completion markers awaiting durable cleanup (a **count**, despite the `_ratio` Prometheus suffix).",
		Group:       GroupSelfObs,
	}
)

// Go runtime metric descriptors. Gauges are point-in-time; the gc/alloc counters
// are monotonic (emitted as per-interval deltas off runtime.MemStats). The
// unit-`1` count gauges become `*_ratio` under Grafana Cloud's OTLP→Prometheus
// normalization (a count, despite the suffix) — the same quirk as
// tailscale2otel.enrich.cache_size.
var (
	DocRuntimeGoroutines = metricdoc.Metric{
		Name:        MetricRuntimeGoroutines,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of live goroutines (a **count**, despite the `_ratio` Prometheus suffix).",
		Group:       GroupSelfObs,
	}
	DocRuntimeGomaxprocs = metricdoc.Metric{
		Name:        MetricRuntimeGomaxprocs,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Current GOMAXPROCS, the max OS threads executing Go code (a **count**, despite the `_ratio` suffix).",
		Group:       GroupSelfObs,
	}
	DocRuntimeHeapAlloc = metricdoc.Metric{
		Name:        MetricRuntimeHeapAlloc,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Bytes of allocated heap objects currently in use.",
		Group:       GroupSelfObs,
	}
	DocRuntimeHeapSys = metricdoc.Metric{
		Name:        MetricRuntimeHeapSys,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Bytes of heap memory obtained from the OS.",
		Group:       GroupSelfObs,
	}
	DocRuntimeHeapInuse = metricdoc.Metric{
		Name:        MetricRuntimeHeapInuse,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Bytes in in-use heap spans.",
		Group:       GroupSelfObs,
	}
	DocRuntimeStackInuse = metricdoc.Metric{
		Name:        MetricRuntimeStackInuse,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Bytes in in-use stack spans.",
		Group:       GroupSelfObs,
	}
	DocRuntimeMemSys = metricdoc.Metric{
		Name:        MetricRuntimeMemSys,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Total bytes of memory obtained from the OS (the process's Go memory footprint).",
		Group:       GroupSelfObs,
	}
	DocRuntimeHeapObjects = metricdoc.Metric{
		Name:        MetricRuntimeHeapObjects,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of live heap objects (a **count**, despite the `_ratio` suffix).",
		Group:       GroupSelfObs,
	}
	DocRuntimeGCNextTarget = metricdoc.Metric{
		Name:        MetricRuntimeGCNextTarget,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Target heap size (bytes) for the next garbage collection.",
		Group:       GroupSelfObs,
	}
	DocRuntimeGCCPUFraction = metricdoc.Metric{
		Name:        MetricRuntimeGCCPUFraction,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Fraction of total CPU time used by the garbage collector since process start (0..1).",
		Group:       GroupSelfObs,
	}
	DocRuntimeGCCount = metricdoc.Metric{
		Name:        MetricRuntimeGCCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Completed garbage-collection cycles since process start.",
		Group:       GroupSelfObs,
	}
	DocRuntimeGCPauseTime = metricdoc.Metric{
		Name:        MetricRuntimeGCPauseTime,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Counter,
		Description: "Cumulative stop-the-world GC pause time since process start.",
		Group:       GroupSelfObs,
	}
	DocRuntimeAllocBytes = metricdoc.Metric{
		Name:        MetricRuntimeAllocBytes,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Cumulative bytes allocated on the heap since process start (includes freed).",
		Group:       GroupSelfObs,
	}
)

// DocComponentErrors is the lifecycle/receiver failure counter descriptor.
var DocComponentErrors = metricdoc.Metric{
	Name:        MetricComponentErrors,
	Unit:        semconv.UnitDimensionless,
	Instrument:  metricdoc.Counter,
	Description: "Failures of non-collector subsystems (receivers, admin server, streaming auto-configure), by component.",
	Attributes:  []string{semconv.AttrComponent},
	Group:       GroupSelfObs,
}

// DocAdminAuthRejected is the admin auth-gate rejection counter descriptor.
var DocAdminAuthRejected = metricdoc.Metric{
	Name:        MetricAdminAuthRejected,
	Unit:        semconv.UnitDimensionless,
	Instrument:  metricdoc.Counter,
	Description: "Admin HTTP requests rejected by the auth gate (status page + pprof), by reason.",
	Attributes:  []string{"reason"},
	Group:       GroupSelfObs,
}

// De-duplication set descriptors.
var (
	DocDedupSize = metricdoc.Metric{
		Name:        MetricDedupSize,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Keys currently held in a cross-source de-duplication set, by set (a **count**, despite the `_ratio` suffix).",
		Attributes:  []string{semconv.AttrDedupSet},
		Group:       GroupSelfObs,
	}
	DocDedupEvictions = metricdoc.Metric{
		Name:        MetricDedupEvictions,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Keys evicted from a de-duplication set because it was at capacity, by set. Steady-state evictions are NORMAL and not a problem: flow dedup keys embed each batch's window timestamps, so keys are effectively unique, and once the fixed-size set first fills it evicts exactly one key per insert forever — even in a perfectly healthy deployment. The real overflow signal is evictions approaching the set's capacity *within a single poll interval* (overlap keys aged out before the next poll can dedup against them, i.e. genuine boundary double-counting), NOT sustained nonzero evictions.",
		Attributes:  []string{semconv.AttrDedupSet},
		Group:       GroupSelfObs,
	}
	DocDedupHits = metricdoc.Metric{
		Name:        MetricDedupHits,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Duplicate keys suppressed by a de-duplication set, by set (a hit is a record dropped because its key was already seen — proves the set is actually de-duplicating; a **count**, despite the `_total` suffix).",
		Attributes:  []string{semconv.AttrDedupSet},
		Group:       GroupSelfObs,
	}
)

// Process metric descriptors. process.uptime is a point-in-time gauge; process.cpu.time
// is a monotonic counter (emitted as per-interval deltas off syscall.Getrusage), split
// by cpu.mode=user|system. Standard OTel names so generic dashboards work out of the box.
var (
	DocProcessUptime = metricdoc.Metric{
		Name:        MetricProcessUptime,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Seconds since the process started (wall-clock uptime).",
		Group:       GroupSelfObs,
	}
	DocProcessCPUTime = metricdoc.Metric{
		Name:        MetricProcessCPUTime,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Counter,
		Description: "Cumulative process CPU time in seconds, by mode (`cpu.mode`=user|system), read from getrusage(RUSAGE_SELF). Emitted on unix platforms only.",
		Attributes:  []string{semconv.AttrCPUMode},
		Group:       GroupSelfObs,
	}
)

// Config-health descriptors. Both are point-in-time gauges re-evaluated each export
// interval. config.warnings is a count (the `_ratio` suffix is a Prom-normalization
// quirk for unit-`1` gauges); config.valid is a 1/0 flag.
var (
	DocConfigWarnings = metricdoc.Metric{
		Name:        MetricConfigWarnings,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of active configuration advisories from config.Warnings() (a **count**, despite the `_ratio` suffix). Non-zero means startup logged WARN-level advisories worth reviewing.",
		Group:       GroupSelfObs,
	}
	DocConfigValid = metricdoc.Metric{
		Name:        MetricConfigValid,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` when the running configuration passes Validate(), else `0` (a **flag**, despite the `_ratio` suffix). Normally `1` at runtime since invalid config fails startup; exposed as an alertable invariant.",
		Group:       GroupSelfObs,
	}
)

// Ingestion-volume descriptors. ingest.records counts records accepted at each
// ingestion-path boundary (post intra-source overlap de-dup for poll; as-received
// for stream/webhook/objectstore). ingest.bytes is the decompressed/decoded
// payload size and is emitted for stream, webhook, and objectstore only (poll has
// no wire body to measure); objectstore's figure is the decompressed object size,
// not the compressed bytes it actually read off the bucket (#450).
var (
	DocIngestRecords = metricdoc.Metric{
		Name:        MetricIngestRecords,
		Unit:        semconv.UnitRecords,
		Instrument:  metricdoc.Counter,
		Description: "Records accepted per ingestion path and signal type. `source`=poll|stream|webhook|objectstore, `signal`=flow|audit|webhook. The unified cross-path ingestion-volume view (the per-path receivers also expose domain counters).",
		Attributes:  []string{semconv.AttrIngestSource, semconv.AttrIngestSignal},
		Group:       GroupSelfObs,
	}
	DocIngestBytes = metricdoc.Metric{
		Name:        MetricIngestBytes,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Decompressed/decoded payload bytes received per ingestion path, by `source`=stream|webhook|objectstore; the poll path has no wire body to measure and emits none. For objectstore this is the decompressed object size (matching stream/webhook's meaning), NOT the compressed bytes actually read from the bucket — see tailscale2otel.objectstore.bytes for that. Note: ingress bytes do not directly drive Grafana Cloud cost — see export.datapoints/export.log_records for that.",
		Attributes:  []string{semconv.AttrIngestSource},
		Group:       GroupSelfObs,
	}
	DocIngestEventAge = metricdoc.Metric{
		Name:        MetricIngestEventAge,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Histogram,
		Description: "Age in seconds of each accepted event at ingestion time, by bounded source and signal. Future event timestamps are counted by ingest.timestamp_skew and clamped to zero.",
		Attributes:  []string{semconv.AttrIngestSource, semconv.AttrIngestSignal},
		Group:       GroupSelfObs,
	}
	DocIngestCaptureDelay = metricdoc.Metric{
		Name:        MetricIngestCaptureDelay,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Histogram,
		Description: "Delay in seconds between an event and its upstream capture/observation timestamp, when the wire format supplies both. Inverted timestamps are counted by ingest.timestamp_skew and clamped to zero.",
		Attributes:  []string{semconv.AttrIngestSource, semconv.AttrIngestSignal},
		Group:       GroupSelfObs,
	}
	DocIngestLastEventTimestamp = metricdoc.Metric{
		Name:        MetricIngestLastEventTimestamp,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Greatest accepted event timestamp as Unix seconds, by bounded source and signal. Delayed retries and backfills do not move this gauge backwards.",
		Attributes:  []string{semconv.AttrIngestSource, semconv.AttrIngestSignal},
		Group:       GroupSelfObs,
	}
	DocIngestTimestampSkew = metricdoc.Metric{
		Name:        MetricIngestTimestampSkew,
		Unit:        "{event}",
		Instrument:  metricdoc.Counter,
		Description: "Accepted events with an inverted timestamp relationship (event after local acceptance, or capture before event), by bounded source and signal.",
		Attributes:  []string{semconv.AttrIngestSource, semconv.AttrIngestSignal},
		Group:       GroupSelfObs,
	}
)

// TLS-certificate-reload descriptors (#316). NotBefore/NotAfter/ReloadedAt are
// all emitted as Unix-seconds gauges (matching DocIngestLastEventTimestamp's
// convention) rather than as a fingerprint or any other identifier, so a
// certificate rotation churns VALUES on a fixed, tiny set of series (one per
// TLS-enabled listener) instead of minting a new series per rotation — which a
// fingerprint-as-label design would do, and which is exactly the kind of label
// churn this codebase avoids elsewhere (see GaugeSnapshot's doc). The
// fingerprint itself is surfaced only on the status page (statusdata.
// TLSListenerStatus), never as a metric attribute.
var (
	DocTLSCertNotBefore = metricdoc.Metric{
		Name:        MetricTLSCertNotBefore,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix seconds of the active TLS certificate's notBefore, by listener component (admin|metrics|stream). Emitted only for a listener with TLS configured; updates on every successful certificate reload.",
		Attributes:  []string{semconv.AttrComponent},
		Group:       GroupSelfObs,
	}
	DocTLSCertNotAfter = metricdoc.Metric{
		Name:        MetricTLSCertNotAfter,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix seconds of the active TLS certificate's notAfter (expiry), by listener component (admin|metrics|stream). Alert on this approaching the current time to catch an expiring certificate before clients start failing handshakes.",
		Attributes:  []string{semconv.AttrComponent},
		Group:       GroupSelfObs,
	}
	DocTLSCertReloadedAt = metricdoc.Metric{
		Name:        MetricTLSCertReloadedAt,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix seconds of the most recent successful TLS certificate reload, by listener component (admin|metrics|stream). A rotated file on disk is picked up on the next handshake at least this recently.",
		Attributes:  []string{semconv.AttrComponent},
		Group:       GroupSelfObs,
	}
	DocTLSCertReloadFailures = metricdoc.Metric{
		Name:        MetricTLSCertReloadFailures,
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "TLS certificate reload attempts that failed to produce a valid keypair, by listener component (admin|metrics|stream). The listener keeps serving the last known-good certificate; a non-zero rate here is the signal that a rotation went wrong.",
		Attributes:  []string{semconv.AttrComponent},
		Group:       GroupSelfObs,
	}
)

// EmitTLSCertLoaded records a successful TLS certificate (re)load for one
// listener component: the certificate's notBefore/notAfter and the reload
// timestamp itself, all as Unix-seconds gauges. See the package doc for why
// this lives here rather than in internal/app.
func EmitTLSCertLoaded(e telemetry.Emitter, component string, notBefore, notAfter, reloadedAt time.Time) {
	e.Gauge(DocTLSCertNotBefore.Name, DocTLSCertNotBefore.Unit, DocTLSCertNotBefore.Description,
		float64(notBefore.Unix()), telemetry.Attrs{semconv.AttrComponent: component})
	e.Gauge(DocTLSCertNotAfter.Name, DocTLSCertNotAfter.Unit, DocTLSCertNotAfter.Description,
		float64(notAfter.Unix()), telemetry.Attrs{semconv.AttrComponent: component})
	e.Gauge(DocTLSCertReloadedAt.Name, DocTLSCertReloadedAt.Unit, DocTLSCertReloadedAt.Description,
		float64(reloadedAt.Unix()), telemetry.Attrs{semconv.AttrComponent: component})
}

// EmitTLSCertReloadFailure records one failed reload attempt (a broken
// replacement that left the previous certificate in service) for one listener
// component.
func EmitTLSCertReloadFailure(e telemetry.Emitter, component string) {
	e.Counter(DocTLSCertReloadFailures.Name, DocTLSCertReloadFailures.Unit, DocTLSCertReloadFailures.Description,
		1, telemetry.Attrs{semconv.AttrComponent: component})
}

// DocSeriesByGroup is the per-group active-series rollup descriptor. A gauge whose
// unit "{series}" annotation is dropped under Prometheus normalization — annotation
// units get no suffix, so the resulting name is the unsuffixed
// tailscale2otel_series_by_group (a plain count; only a unit-"1" gauge would get
// the "_ratio" suffix).
var DocSeriesByGroup = metricdoc.Metric{
	Name:        MetricSeriesByGroup,
	Unit:        semconv.UnitSeries,
	Instrument:  metricdoc.Gauge,
	Description: "Active time series emitted during the last export interval, summed by the catalog group that owns each metric (a roll-up of tailscale2otel.series.active by `metric.group`). Uncataloged metric names (e.g. node-metrics passthrough) bucket under `other`. A **count**.",
	Attributes:  []string{semconv.AttrMetricGroup},
	Group:       GroupSelfObs,
}

// DocPIIFilterCategory is the PII-redaction-state gauge descriptor. One datapoint
// per category; value 1 means the category is emitted, 0 means it is redacted.
// Emitted only when self_observability.enabled is true.
var DocPIIFilterCategory = metricdoc.Metric{
	Name:        MetricPIIFilterCategory,
	Unit:        "1",
	Instrument:  metricdoc.Gauge,
	Description: "PII redaction state per category: `1` = emitted, `0` = redacted (a **flag**, despite the `_ratio` Prometheus suffix). One datapoint per category, emitted each interval so dashboards can conditionally render PII-bearing panels.",
	Attributes:  []string{"category"},
	Group:       GroupSelfObs,
}

// Catalog returns the self-observability metrics the app layer emits, for the
// docs generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		DocUp, DocUpdateAvailable, DocAPIRequests, DocAPIRetries, DocAPIDuration, DocAPIRateLimitWait,
		DocRuntimeGoroutines, DocRuntimeGomaxprocs,
		DocRuntimeHeapAlloc, DocRuntimeHeapSys, DocRuntimeHeapInuse, DocRuntimeStackInuse, DocRuntimeMemSys,
		DocRuntimeHeapObjects, DocRuntimeGCNextTarget, DocRuntimeGCCPUFraction,
		DocRuntimeGCCount, DocRuntimeGCPauseTime, DocRuntimeAllocBytes,
		DocComponentErrors,
		DocAdminAuthRejected,
		DocDedupSize, DocDedupEvictions, DocDedupHits,
		DocProcessUptime, DocProcessCPUTime,
		DocConfigWarnings, DocConfigValid,
		DocIngressWALPendingEntries, DocIngressWALPendingSize,
		DocIngressWALOrphanStages, DocIngressWALOrphanSize, DocIngressWALCompletionMarkers,
		DocIngestRecords, DocIngestBytes, DocIngestEventAge, DocIngestCaptureDelay,
		DocIngestLastEventTimestamp, DocIngestTimestampSkew,
		DocSeriesByGroup,
		DocPIIFilterCategory,
		DocTLSCertNotBefore, DocTLSCertNotAfter, DocTLSCertReloadedAt, DocTLSCertReloadFailures,
		DocProfilingUploadAttempts, DocProfilingUploadFailures, DocProfilingUploadDuration,
		DocProfilingUploadLastSuccess, DocProfilingUploadConsecutiveFailures,
		DocMetricsScrapeRequests, DocMetricsScrapeInFlight, DocMetricsScrapeDuration,
		DocMetricsScrapeGatherErrors, DocMetricsAuthRejected,
	}
}

// LogCatalog returns the log events the app layer emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}

// Capability-matrix metric source names (#425/#430). The matrix itself is built
// in internal/app; only its descriptors live here, because internal/catalog
// aggregates every package's Catalog() and must never import internal/app —
// internal/app imports internal/catalog to render the admin status page, so the
// dependency has to stay one-way.
const (
	// MetricCapabilityStatus is the per-collector capability state gauge.
	MetricCapabilityStatus = "tailscale2otel.capability.status"
	// MetricCapabilityScopeSatisfied is the scope-preflight flag gauge.
	MetricCapabilityScopeSatisfied = "tailscale2otel.capability.scope_satisfied"
)

var (
	// DocCapabilityStatus documents the per-collector capability state gauge.
	DocCapabilityStatus = metricdoc.Metric{
		Name:       MetricCapabilityStatus,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "`1` for the enabled collector's current capability state, `0` for every other state " +
			"(a **flag**, despite the `_ratio` Prometheus suffix). The full state set is zero-seeded, so a " +
			"collector that recovers falls to `0` on its old state instead of pinning there forever. The value " +
			"is the most operator-relevant state across all of that collector's probed operations, so a partial " +
			"`scope_denied` is never masked by a sibling success. Use it to render an optional-feature panel's " +
			"empty state: `disabled` means the tailnet does not have the feature, `scope_denied` means the " +
			"credential cannot see it.",
		Attributes: []string{semconv.AttrCollector, semconv.AttrAPIState},
		Group:      GroupSelfObs,
	}
	// DocCapabilityScopeSatisfied documents the advisory scope-preflight flag.
	DocCapabilityScopeSatisfied = metricdoc.Metric{
		Name:       MetricCapabilityScopeSatisfied,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "`1` when the OAuth scopes requested in configuration cover the capability's documented " +
			"requirement, `0` when they demonstrably do not (a **flag**, despite the `_ratio` suffix). " +
			"**Advisory startup preflight, not a server answer**: it compares `tailscale.auth.oauth.scopes` " +
			"against a static map of upstream's documented scopes, so it never blocks collection and a `1` is " +
			"not a guarantee. Emitted only for capabilities whose scope is modeled AND when the credential is " +
			"an OAuth client — an API key carries no scope list, and a `0` there would read as a real permission " +
			"gap. The authoritative signal is `tailscale2otel.capability.status` / " +
			"`tailscale2otel.api.availability` reaching `scope_denied`.",
		Attributes: []string{semconv.AttrCapability},
		Group:      GroupSelfObs,
	}
)

// CapabilityCatalog returns the capability-matrix descriptors.
func CapabilityCatalog() []metricdoc.Metric {
	return []metricdoc.Metric{DocCapabilityStatus, DocCapabilityScopeSatisfied}
}
