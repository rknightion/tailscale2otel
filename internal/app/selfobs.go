package app

import (
	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// apiObserver returns a tsapi request-observer that records one
// tailscale2otel.api.requests increment per request (keyed by endpoint and
// status code) and, when a request was retried, the retry count on
// tailscale2otel.api.retries. It is wired into tsapi only when
// self-observability is enabled. The metric descriptors live in
// internal/appcatalog (see that package for why).
func apiObserver(e telemetry.Emitter) func(endpoint string, status, attempts int) {
	return func(endpoint string, status, attempts int) {
		e.Counter(appcatalog.DocAPIRequests.Name, appcatalog.DocAPIRequests.Unit, appcatalog.DocAPIRequests.Description, 1,
			telemetry.Attrs{
				"endpoint":                  endpoint,
				"http.response.status_code": status,
			})
		if attempts > 1 {
			e.Counter(appcatalog.DocAPIRetries.Name, appcatalog.DocAPIRetries.Unit, appcatalog.DocAPIRetries.Description,
				float64(attempts-1), telemetry.Attrs{"endpoint": endpoint})
		}
	}
}
