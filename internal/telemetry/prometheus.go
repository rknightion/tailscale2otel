package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// attrProvider is the resource attribute key set by buildResource for the
// control-plane backend (tailscale|headscale). No semconv constant exists for it
// (it is a literal in buildResource), so it is named here for the promotion filter.
const attrProvider = "tailscale2otel.provider"

// promoteResourceAttr selects the Resource attributes promoted to CONSTANT LABELS
// on every Prometheus series: tailscale.tailnet and tailscale2otel.provider. This
// disambiguates multi-tailnet series on the shared /metrics page (each tailnet
// provider stamps tailscale_tailnet=<name>) — the process provider has neither, so
// its process-global series stay unlabelled. Interim shim that roadmap item L's
// metric-attribute change later subsumes (drop this option, the label then comes
// from the data-point attribute).
func promoteResourceAttr(kv attribute.KeyValue) bool {
	return kv.Key == attribute.Key(semconv.AttrTailnet) || kv.Key == attribute.Key(attrProvider)
}

// newPrometheusReader builds the OTEL Prometheus exporter (a sdkmetric.Reader)
// bound to reg, with the otel_scope_* metric/labels dropped (verified: 0 refs in
// deploy/) and tailnet/provider promoted to constant labels. target_info is kept
// (exporter default).
func newPrometheusReader(reg prometheus.Registerer) (sdkmetric.Reader, error) {
	return otelprom.New(
		otelprom.WithRegisterer(reg),
		otelprom.WithoutScopeInfo(),
		otelprom.WithResourceAsConstantLabels(promoteResourceAttr),
	)
}
