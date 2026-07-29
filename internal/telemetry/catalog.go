package telemetry

import (
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's
// self-observability metric documentation: name, unit, instrument, description,
// and attribute keys. The emit sites (selfobs.go) reference these descriptors so
// a description/unit cannot drift from what is documented, and the doc generator
// (tools/metricscatalog, via internal/catalog) renders them into docs/metrics.md.
// A consistency test (catalog_test.go) asserts what these helpers actually emit
// matches these declarations.
//
// These metrics share the cross-cutting "Self-observability" doc section with
// the scrape.* metrics (internal/collector), api.* + up (internal/app), and the
// enrich.cache_* metrics (internal/collector/devices).
const groupSelfObs = "Self-observability"

var (
	docBuildInfo = metricdoc.Metric{
		Name:        "tailscale2otel.build_info",
		Unit:        "1",
		Instrument:  metricdoc.Gauge,
		Description: "Constant `1` build-info gauge carrying the build version as the `version` label and the Go runtime version as `go.version`. This is the metrics-side home of the service version: it is kept off the resource (and so off every series as `service_version`) — join it with `group_left` to attribute other metrics to a build.",
		Attributes:  []string{"version", "go.version"},
		Group:       groupSelfObs,
	}
	// A log record is bounded before export (#366) so one oversized HEC, audit
	// or webhook record cannot dominate a batch or breach a backend's per-record
	// limit. Truncation is silent on the wire apart from the marker, so these two
	// counters are the only way to notice it is happening. `field` is a closed
	// set ("body" or "attribute"), never derived from record content.
	docLogRecordTruncated = metricdoc.Metric{
		Name:        "tailscale2otel.log.record.truncated",
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "Count of log records whose body or an attribute value was truncated to a bounded length before export, by field.",
		Attributes:  []string{"field"},
		Group:       groupSelfObs,
	}
	docLogTruncatedBytes = metricdoc.Metric{
		Name:        "tailscale2otel.log.truncated.bytes",
		Unit:        "By",
		Instrument:  metricdoc.Counter,
		Description: "Bytes dropped from log record bodies/attribute values by truncation, by field.",
		Attributes:  []string{"field"},
		Group:       groupSelfObs,
	}
	docExportFailures = metricdoc.Metric{
		Name:        "tailscale2otel.export.failures",
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "OTLP export failures, by error class.",
		Attributes:  []string{semconv.AttrErrorType, semconv.AttrExportSignal},
		Group:       groupSelfObs,
	}
	// docExportSpans is the trace-side sibling of docExportDatapoints /
	// docExportLogRecords (#359): before this, delivery_trace.go's exporter
	// observed trace DELIVERY (success/failure) but counted nothing, so there was
	// no cost-proxy tally for spans handed to the OTLP trace exporter at all.
	docExportSpans = metricdoc.Metric{
		Name: "tailscale2otel.export.spans",
		// No dedicated UCUM/annotation unit constant exists for "spans" in
		// internal/semconv yet (out of this issue's owned files — see its
		// WIRING REQUEST to add semconv.UnitSpans = "{span}"); using the literal
		// directly keeps this metric's own unit internally consistent in the
		// meantime (the Metric.Unit here and the Counter call in
		// EmitExportSpanDelta both reference this same field).
		Unit:        "{span}",
		Instrument:  metricdoc.Counter,
		Description: "Spans handed to the OTLP trace exporter (the trace cost proxy). Counts every span per export batch.",
		Group:       groupSelfObs,
	}
	// docExportDiagnosticsSuppressed is the #365 companion to the outage-diagnostic
	// logging in delivery.go: while a signal's OTLP export failures are being
	// rate-limited to a first-occurrence log plus periodic summaries, this counter
	// increments EXACTLY once per suppressed log line, so "how many failures did we
	// not individually log" is always answerable even though the log itself is not.
	docExportDiagnosticsSuppressed = metricdoc.Metric{
		Name:        "tailscale2otel.export.diagnostics.suppressed",
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "Export-failure diagnostic log lines suppressed during a sustained OTLP outage, by signal and error class. Exact — never itself rate-limited.",
		Attributes:  []string{semconv.AttrExportSignal, semconv.AttrErrorType},
		Group:       groupSelfObs,
	}
	docExportDatapoints = metricdoc.Metric{
		Name:        "tailscale2otel.export.datapoints",
		Unit:        semconv.UnitDataPoints,
		Instrument:  metricdoc.Counter,
		Description: "Metric data points handed to the OTLP metric exporter (the DPM cost proxy). Counts every point across all instruments per export cycle; includes this self-metric (+1/cycle).",
		Group:       groupSelfObs,
	}
	docExportLogRecords = metricdoc.Metric{
		Name:        "tailscale2otel.export.log_records",
		Unit:        semconv.UnitRecords,
		Instrument:  metricdoc.Counter,
		Description: "Log records handed to the OTLP log exporter (the log-volume cost driver; flow/audit logs dominate). Counts every record per export batch.",
		Group:       groupSelfObs,
	}
	docExportDuration = metricdoc.Metric{
		Name:        "tailscale2otel.export.duration",
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Histogram,
		Description: "Wall-clock duration of each OTLP `Export()` call to the backend, by signal and outcome. `signal`=metrics|logs, `outcome`=success|failure. One observation per export cycle per signal; use it for export-latency p50/p99 and to tell a slow backend from a failing one.",
		Attributes:  []string{semconv.AttrExportSignal, semconv.AttrExportOutcome},
		Group:       groupSelfObs,
	}
	docSeriesActive = metricdoc.Metric{
		Name:        seriesActiveMetric,
		Unit:        semconv.UnitSeries,
		Instrument:  metricdoc.Gauge,
		Description: "Exact distinct active time series emitted for `metric.name` during the last export interval; bounded by a per-metric cap (the value pins at the cap when exceeded). A **count**.",
		Attributes:  []string{semconv.AttrMetricName},
		Group:       groupSelfObs,
	}
	docSeriesLimit = metricdoc.Metric{
		Name:        seriesLimitMetric,
		Unit:        semconv.UnitSeries,
		Instrument:  metricdoc.Gauge,
		Description: "Effective per-metric active-series cap (`cardinality.metric_limit`): the point at which excess series collapse into `otel_metric_overflow` (silent per-series loss). Emitted only when a positive limit is configured. A **count**.",
		Group:       groupSelfObs,
	}
	docSeriesOverflowing = metricdoc.Metric{
		Name:        seriesOverflowMetric,
		Unit:        "1",
		Instrument:  metricdoc.Gauge,
		Description: "1 when `metric.name` reached the per-metric series cap during the last interval (excess series silently dropped into `otel_metric_overflow`), else 0. Always 0 when no positive `cardinality.metric_limit` is configured.",
		Attributes:  []string{semconv.AttrMetricName},
		Group:       groupSelfObs,
	}
)

// Catalog returns the self-observability metrics this package emits, for the doc
// generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docBuildInfo, docExportFailures, docExportDatapoints, docExportLogRecords, docExportSpans,
		docExportDuration, docExportDiagnosticsSuppressed,
		docSeriesActive, docSeriesLimit, docSeriesOverflowing,
		docLogRecordTruncated, docLogTruncatedBytes,
		// Declared in processors.go, registered here: the registry is the one
		// place every emitting file's descriptors converge, so a descriptor
		// declared but never registered emits fine and is silently undocumented.
		docQueueSize, docQueueCapacity, docQueueDropped,
	}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
