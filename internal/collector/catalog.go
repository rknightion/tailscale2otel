package collector

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for the scheduler's
// per-collector scrape.* self-observability metric documentation. The emit
// sites (selfobs.go) reference these descriptors so the unit/description cannot
// drift from what is documented; the doc generator (tools/metricscatalog, via
// internal/catalog) renders them into docs/metrics.md, and catalog_test.go
// asserts what emitScrapeMetrics emits matches these declarations.
//
// These share the cross-cutting "Self-observability" doc section with the
// telemetry build/export metrics, the app api.*/up metrics, and the devices
// enrich.cache_* metrics.
const groupSelfObs = "Self-observability"

var (
	docScrapeDuration = metricdoc.Metric{
		Name:        MetricScrapeDuration,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Wall-clock duration of the last scrape, per collector.",
		Attributes:  []string{semconv.AttrCollector},
		Group:       groupSelfObs,
	}
	docScrapeSuccess = metricdoc.Metric{
		Name:        MetricScrapeSuccess,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if the last scrape for that collector succeeded, else `0`.",
		Attributes:  []string{semconv.AttrCollector},
		Group:       groupSelfObs,
	}
	docScrapeErrors = metricdoc.Metric{
		Name:        MetricScrapeErrors,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Count of scrape errors, by collector and error class.",
		Attributes:  []string{semconv.AttrCollector, "error.type"},
		Group:       groupSelfObs,
	}
	docScrapeLastTimestamp = metricdoc.Metric{
		Name:        MetricScrapeLastTimestamp,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp the last scrape *finished* (success **or** failure); pair with `scrape.success` to detect last-success staleness.",
		Attributes:  []string{semconv.AttrCollector},
		Group:       groupSelfObs,
	}
)

// Catalog returns the self-observability metrics this package emits, for the doc
// generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docScrapeDuration, docScrapeSuccess, docScrapeErrors, docScrapeLastTimestamp}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
