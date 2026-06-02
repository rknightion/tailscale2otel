package app

import "github.com/rknightion/tailscale2otel/internal/telemetry"

// Self-observability metric names emitted from the app layer (the scheduler and
// collectors emit the rest; see internal/collector and internal/telemetry).
const (
	metricAPIRequests = "tailscale2otel.api.requests"
	metricAPIRetries  = "tailscale2otel.api.retries"
)

// apiObserver returns a tsapi request-observer that records one
// tailscale2otel.api.requests increment per request (keyed by endpoint and
// status code) and, when a request was retried, the retry count on
// tailscale2otel.api.retries. It is wired into tsapi only when
// self-observability is enabled.
func apiObserver(e telemetry.Emitter) func(endpoint string, status, attempts int) {
	return func(endpoint string, status, attempts int) {
		e.Counter(metricAPIRequests, "1", "Tailscale API requests by endpoint and status", 1,
			telemetry.Attrs{
				"endpoint":                  endpoint,
				"http.response.status_code": status,
			})
		if attempts > 1 {
			e.Counter(metricAPIRetries, "1", "Tailscale API request retries by endpoint",
				float64(attempts-1), telemetry.Attrs{"endpoint": endpoint})
		}
	}
}
