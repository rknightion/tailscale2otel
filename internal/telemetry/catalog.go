package telemetry

import (
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
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
	docExportFailures = metricdoc.Metric{
		Name:        "tailscale2otel.export.failures",
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "OTLP export failures, by error class.",
		Attributes:  []string{"error.type"},
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
	return []metricdoc.Metric{docBuildInfo, docExportFailures, docExportDatapoints, docExportLogRecords, docExportDuration, docSeriesActive, docSeriesLimit, docSeriesOverflowing}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
