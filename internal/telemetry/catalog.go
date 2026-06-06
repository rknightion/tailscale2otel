package telemetry

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
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
		Description: "Constant `1` build-info gauge; the Go runtime version is carried as the `go.version` label (the service version is promoted from the resource as `service_version`).",
		Attributes:  []string{"go.version"},
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
	docSeriesActive = metricdoc.Metric{
		Name:        seriesActiveMetric,
		Unit:        semconv.UnitSeries,
		Instrument:  metricdoc.Gauge,
		Description: "Exact distinct active time series emitted for `metric.name` during the last export interval; bounded by a per-metric cap (the value pins at the cap when exceeded). A **count**.",
		Attributes:  []string{semconv.AttrMetricName},
		Group:       groupSelfObs,
	}
)

// Catalog returns the self-observability metrics this package emits, for the doc
// generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docBuildInfo, docExportFailures, docSeriesActive}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
