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
package appcatalog

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
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
const (
	ComponentStream        = "stream"
	ComponentWebhook       = "webhook"
	ComponentAdmin         = "admin"
	ComponentAutoConfigure = "auto_configure"
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
		Description: "Tailscale API request wall-clock latency in seconds, by endpoint and HTTP status code. Covers the full logical request including any retry backoff (not just server time). Use the 429 status-code bucket here plus tailscale2otel.api.retries for rate-limit visibility — the Tailscale API exposes no rate-limit-remaining headers.",
		Attributes:  []string{"endpoint", "http.response.status_code"},
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
		Description: "Keys evicted from a de-duplication set because it was at capacity, by set (sustained growth means the set is undersized).",
		Attributes:  []string{semconv.AttrDedupSet},
		Group:       GroupSelfObs,
	}
)

// Catalog returns the self-observability metrics the app layer emits, for the
// docs generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		DocUp, DocUpdateAvailable, DocAPIRequests, DocAPIRetries, DocAPIDuration,
		DocRuntimeGoroutines, DocRuntimeGomaxprocs,
		DocRuntimeHeapAlloc, DocRuntimeHeapSys, DocRuntimeHeapInuse, DocRuntimeStackInuse, DocRuntimeMemSys,
		DocRuntimeHeapObjects, DocRuntimeGCNextTarget, DocRuntimeGCCPUFraction,
		DocRuntimeGCCount, DocRuntimeGCPauseTime, DocRuntimeAllocBytes,
		DocComponentErrors,
		DocAdminAuthRejected,
		DocDedupSize, DocDedupEvictions,
	}
}

// LogCatalog returns the log events the app layer emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
