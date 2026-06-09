package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// newPrometheusReader builds the OTEL Prometheus exporter (a sdkmetric.Reader)
// bound to reg, with the otel_scope_* metric/labels dropped (verified: 0 refs in
// deploy/). tailnet/provider are emitted as data-point attributes (roadmap item L),
// so no resource-promotion shim is needed — they appear as real labels on every
// series directly; target_info is kept (exporter default).
func newPrometheusReader(reg prometheus.Registerer) (sdkmetric.Reader, error) {
	return otelprom.New(
		otelprom.WithRegisterer(reg),
		otelprom.WithoutScopeInfo(),
	)
}
