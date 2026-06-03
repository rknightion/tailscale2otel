package dns

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation: name, unit, instrument, and description. The emit sites
// (dns.go) reference these descriptors so a description/unit cannot drift from
// what is documented; the doc generator (tools/metricscatalog, via
// internal/catalog) renders them into docs/metrics.md, and catalog_test.go
// asserts what the collector emits matches these declarations.
const groupDNS = "DNS"

var (
	docNameserversCount = metricdoc.Metric{
		Name:        metricNameserversCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of configured nameservers (a **count**).",
		Group:       groupDNS,
	}
	docSearchPathsCount = metricdoc.Metric{
		Name:        metricSearchPathsCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of DNS search paths (a **count**).",
		Group:       groupDNS,
	}
	docSplitZonesCount = metricdoc.Metric{
		Name:        metricSplitZonesCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of split-DNS zones configured (a **count**).",
		Group:       groupDNS,
	}
	docMagicDNS = metricdoc.Metric{
		Name:        metricMagicDNS,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if MagicDNS is enabled, else `0`.",
		Group:       groupDNS,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docNameserversCount, docSearchPathsCount, docSplitZonesCount, docMagicDNS}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
