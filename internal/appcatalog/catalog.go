// Package appcatalog holds the app layer's self-observability metric
// descriptors (the heartbeat up gauge and the Tailscale API request/retry
// counters) as the SINGLE SOURCE OF TRUTH for both their emission and their
// documentation.
//
// It lives in its own leaf package — rather than in internal/app — so that
// internal/catalog (the docs aggregator) can pull these descriptors WITHOUT
// importing internal/app. That keeps internal/app free to import internal/catalog
// itself (the admin status page renders the full catalog), which a direct
// catalog->app dependency would forbid.
//
// The app-layer emit sites (internal/app/heartbeat.go, internal/app/selfobs.go)
// reference these descriptors so the emitted unit/description cannot drift from
// what is documented; internal/app/catalog_test.go is the drift guard. These
// share the cross-cutting "Self-observability" doc section with the telemetry
// build/export/cardinality metrics, the collector scrape.* metrics, and the
// devices enrich.cache_* metrics.
package appcatalog

import "github.com/rknightion/tailscale2otel/internal/metricdoc"

// GroupSelfObs is the docs section these metrics render under.
const GroupSelfObs = "Self-observability"

// Self-observability metric names emitted from the app layer (the scheduler and
// collectors emit the rest; see internal/collector and internal/telemetry).
const (
	// MetricUp is the heartbeat liveness gauge name.
	MetricUp = "tailscale2otel.up"
	// MetricAPIRequests counts Tailscale API requests.
	MetricAPIRequests = "tailscale2otel.api.requests"
	// MetricAPIRetries counts Tailscale API retry attempts.
	MetricAPIRetries = "tailscale2otel.api.retries"
)

// Descriptors for the app layer's self-observability metrics. Exported so the
// emit sites in package app can reference them.
var (
	DocUp = metricdoc.Metric{
		Name:        MetricUp,
		Unit:        "1",
		Instrument:  metricdoc.Gauge,
		Description: "Liveness flag: `1` while the service is running and reporting.",
		Group:       GroupSelfObs,
	}
	DocAPIRequests = metricdoc.Metric{
		Name:        MetricAPIRequests,
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "Tailscale API requests, by endpoint and HTTP status code.",
		Attributes:  []string{"endpoint", "http.response.status_code"},
		Group:       GroupSelfObs,
	}
	DocAPIRetries = metricdoc.Metric{
		Name:        MetricAPIRetries,
		Unit:        "1",
		Instrument:  metricdoc.Counter,
		Description: "API retry attempts, by endpoint.",
		Attributes:  []string{"endpoint"},
		Group:       GroupSelfObs,
	}
)

// Catalog returns the self-observability metrics the app layer emits, for the
// docs generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{DocUp, DocAPIRequests, DocAPIRetries}
}

// LogCatalog returns the log events the app layer emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
