package telemetry

import "testing"

// TestOTLPHTTPURL pins the OTLP/HTTP per-signal URL construction. The OTEL Go
// otlphttp exporter's WithEndpointURL uses the URL path AS-IS (it does not
// append /v1/<signal>), so a Grafana Cloud base endpoint like ".../otlp" must
// have the signal path appended or the gateway returns 404.
func TestOTLPHTTPURL(t *testing.T) {
	cases := []struct {
		base, signal, want string
	}{
		{"https://otlp-gateway-prod-gb-south-1.grafana.net/otlp", "metrics", "https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/v1/metrics"},
		{"https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/", "logs", "https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/v1/logs"},
		{"https://x/otlp/v1/metrics", "metrics", "https://x/otlp/v1/metrics"}, // already signal-specific: no double-append
		{"http://collector:4318", "metrics", "http://collector:4318/v1/metrics"},
	}
	for _, c := range cases {
		if got := otlpHTTPURL(c.base, c.signal); got != c.want {
			t.Errorf("otlpHTTPURL(%q, %q) = %q, want %q", c.base, c.signal, got, c.want)
		}
	}
}
