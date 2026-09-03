package services

import (
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation; the emit sites reference these descriptors so a description/
// unit cannot drift, and catalog_test.go asserts the emission matches.
const groupServices = "Services"

var (
	docCount = metricdoc.Metric{
		Name:        metricCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of Tailscale Services (VIP services) in the tailnet (a **count**, despite `_ratio`).",
		Group:       groupServices,
	}
	docByTag = metricdoc.Metric{
		Name:        metricByTag,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of Tailscale Services carrying each ACL tag (a service with N tags counts in N series). **Gated** by `collect_tag_rollup`; capped by `tag_rollup_limit` with overflow tags folded into `tailscale.tag=\"__other__\"`.",
		Attributes:  []string{attrTag},
		Group:       groupServices,
	}
	docPorts = metricdoc.Metric{
		Name:        metricPorts,
		Unit:        semconv.UnitPorts,
		Instrument:  metricdoc.Gauge,
		Description: "Number of port rules exposed by a Tailscale Service; one series per service, carrying its optional display name. **Gated** by `cardinality.per_entity.service`.",
		Attributes:  []string{attrName, attrDisplayName},
		Group:       groupServices,
	}
	docHosts = metricdoc.Metric{
		Name:        metricHosts,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Backing-host **count** for a Tailscale Service, bucketed by approval + configured state and carrying its optional display name; one series per service/approval/configured. **Gated** by `collect_hosts` (N+1 calls) and `cardinality.per_entity.service`.",
		Attributes:  []string{attrName, attrDisplayName, attrApproval, attrConfigured},
		Group:       groupServices,
	}
	docHostInfo = metricdoc.Metric{
		Name:        metricHostInfo,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Info gauge (constant `1`) for each device backing a Tailscale Service, carrying the service name/display name, approval/configured state, the host's `tailscale.node.id`, and joined device identity when the devices cache contains that node. **Gated** by `collect_hosts` and `cardinality.per_entity.service`.",
		Attributes: []string{
			attrName, attrDisplayName, attrNodeID, attrApproval, attrConfigured,
			semconv.HostName, semconv.HostID, semconv.OSType, semconv.OSVersion,
			semconv.AttrUser, semconv.AttrTags,
		},
		Group: groupServices,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docCount, docByTag, docPorts, docHosts, docHostInfo}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent { return nil }
