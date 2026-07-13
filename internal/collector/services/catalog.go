package services

import (
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
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
	docPorts = metricdoc.Metric{
		Name:        metricPorts,
		Unit:        semconv.UnitPorts,
		Instrument:  metricdoc.Gauge,
		Description: "Number of port rules exposed by a Tailscale Service; one series per service. **Gated** by `cardinality.per_entity.service`.",
		Attributes:  []string{attrName},
		Group:       groupServices,
	}
	docHosts = metricdoc.Metric{
		Name:        metricHosts,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Backing-host **count** for a Tailscale Service, bucketed by approval + configured state; one series per service/approval/configured. **Gated** by `collect_hosts` (N+1 calls) and `cardinality.per_entity.service`.",
		Attributes:  []string{attrName, attrApproval, attrConfigured},
		Group:       groupServices,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric { return []metricdoc.Metric{docCount, docPorts, docHosts} }

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent { return nil }
