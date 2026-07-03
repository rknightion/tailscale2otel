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
	docOverrideLocal = metricdoc.Metric{
		Name:        metricOverrideLocal,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if Tailscale DNS resolvers override the local OS DNS configuration (`preferences.overrideLocalDNS`), else `0`.",
		Group:       groupDNS,
	}
	docUseWithExitNode = metricdoc.Metric{
		Name:        metricUseWithExitNode,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of DNS resolvers (global + split-DNS) set to remain in use under an exit node (`useWithExitNode`, Tailscale v1.88.1+; a **count**).",
		Group:       groupDNS,
	}
	docResolver = metricdoc.Metric{
		Name:        metricResolver,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Info gauge (always `1`) for each configured DNS resolver, labeled by `address`, `kind` (`global`|`split`), split-DNS `domain` (empty for global), and `use_with_exit_node`. A split-DNS domain configured with a null/empty resolver list still emits one point here with `address` empty, so every domain counted in `tailscale.dns.split_zones.count` has an identifiable series.",
		Attributes:  []string{attrAddress, attrKind, attrDomain, attrUseWithExitNode},
		Group:       groupDNS,
	}
	docSearchPath = metricdoc.Metric{
		Name:        metricSearchPath,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Info gauge (always `1`) for each configured DNS search path, labeled by `domain`.",
		Attributes:  []string{attrSearchPathDomain},
		Group:       groupDNS,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docNameserversCount, docSearchPathsCount, docSplitZonesCount, docMagicDNS,
		docOverrideLocal, docUseWithExitNode, docResolver, docSearchPath,
	}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
