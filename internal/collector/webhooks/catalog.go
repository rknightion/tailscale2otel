package webhooks

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation; the emit site (webhooks.go) references these descriptors so a
// description/unit cannot drift, and catalog_test.go asserts the emission matches.
const groupWebhooks = "Webhooks"

var (
	docEndpointsCount = metricdoc.Metric{
		Name:        metricEndpointsCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of configured webhook endpoints (a **count**, despite `_ratio`).",
		Group:       groupWebhooks,
	}
	docEndpointSubs = metricdoc.Metric{
		Name:        metricEndpointSubs,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of event categories a webhook endpoint is subscribed to (a **count**); one series per endpoint. **Gated** by `cardinality.webhook_per_entity`. The endpoint URL/secret/creator are never emitted.",
		Attributes:  []string{attrEndpointID, attrEndpointProvider},
		Group:       groupWebhooks,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric { return []metricdoc.Metric{docEndpointsCount, docEndpointSubs} }

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent { return nil }
