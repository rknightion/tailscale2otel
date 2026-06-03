package app

import "github.com/rknightion/tailscale2otel/internal/metricdoc"

// Catalog declarations are the SINGLE SOURCE OF TRUTH for the app layer's
// self-observability metric documentation. The emit sites (heartbeat.go,
// selfobs.go) reference these descriptors so the unit/description cannot drift
// from what is documented; the doc generator (tools/metricscatalog, via
// internal/catalog) renders them into docs/metrics.md, and catalog_test.go
// asserts what the helpers emit matches these declarations.
//
// These share the cross-cutting "Self-observability" doc section with the
// telemetry build/export metrics, the collector scrape.* metrics, and the
// devices enrich.cache_* metrics.
const groupSelfObs = "Self-observability"

// metricUp is the heartbeat liveness gauge name.
const metricUp = "tailscale2otel.up"

var (
	docUp = metricdoc.Metric{
		Name:        metricUp,
		Unit:        "1",
		Instrument:  metricdoc.Gauge,
		Description: "Liveness flag: `1` while the service is running and reporting.",
		Group:       groupSelfObs,
	}
	docAPIRequests = metricdoc.Metric{
		Name:        metricAPIRequests,
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "Tailscale API requests, by endpoint and HTTP status code.",
		Attributes:  []string{"endpoint", "http.response.status_code"},
		Group:       groupSelfObs,
	}
	docAPIRetries = metricdoc.Metric{
		Name:        metricAPIRetries,
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "API retry attempts, by endpoint.",
		Attributes:  []string{"endpoint"},
		Group:       groupSelfObs,
	}
)

// Catalog returns the self-observability metrics this package emits, for the doc
// generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docUp, docAPIRequests, docAPIRetries}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
