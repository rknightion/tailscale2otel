package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/otlptranslator"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// PromTranslationStrategy is the OTLP→Prometheus translation strategy this
// project pins on the pull-path (`/metrics`) exporter. It is selected
// EXPLICITLY rather than inherited from the exporter's default, because the
// default is not stable: `otelprom.WithTranslationStrategy`'s own doc comment
// says the zero value resolves from `prometheus/common/model`'s
// NameValidationScheme ("legacy" → UnderscoreEscapingWithSuffixes, "utf8" →
// NoUTF8EscapingWithSuffixes) and that a future release will change the
// default. In the pinned exporter v0.66.0 the zero value happens to resolve to
// UnderscoreEscapingWithSuffixes unconditionally (config.go newConfig), so
// pinning it here is a behavioral no-op TODAY — it is a guard against an
// upstream default flip silently renaming every series on the pull endpoint.
//
// Concretely this strategy means (see prometheus/otlptranslator v1.0.0
// normalizeName):
//   - every rune outside [A-Za-z0-9:] becomes '_' and runs of them collapse;
//   - a known UCUM unit contributes a suffix token (By→bytes, s→seconds,
//     d→days); annotation units in {curly braces} contribute nothing;
//   - a monotonic counter gets a `total` token appended;
//   - a unit-"1" GAUGE gets a `ratio` token appended (a unit-"1" counter does
//     not — it just gets `total`);
//   - CRUCIALLY, each of those tokens is DEDUPED against the tokens already in
//     the metric name. `tailscale2otel.objectstore.bytes` with unit `By` stays
//     `tailscale2otel_objectstore_bytes_total`, NOT `..._bytes_bytes_total`.
//     `metricdoc.PromName` implements the same rule by delegating to the same
//     library — keep them in lockstep.
//
// Relationship to the OTLP PUSH path (Grafana Cloud / Mimir): Mimir's OTLP
// ingest uses this same prometheus/otlptranslator library, so the metric-name
// mapping agrees. The paths are NOT identical elsewhere, and code must not
// assume a series looks the same on both:
//   - resource attributes: the pull path emits ALL of them on `target_info`
//     and promotes none onto series, while Grafana Cloud promotes the whole
//     `service.*` namespace onto every series (job/instance plus service_*).
//     See internal/telemetry/CLAUDE.md.
//   - `tailscale.tailnet` / `tailscale2otel.provider` are per-data-point
//     attributes here precisely so both paths label them identically.
//   - the pull path adds no `_created` series and applies no staleness marker;
//     the push path carries no staleness signal at all.
//   - Mimir may additionally apply per-tenant relabelling/name validation that
//     this exporter never sees.
var PromTranslationStrategy = otlptranslator.UnderscoreEscapingWithSuffixes

// newPrometheusReader builds the OTEL Prometheus exporter (a sdkmetric.Reader)
// bound to reg, with the otel_scope_* metric/labels dropped (verified: 0 refs in
// deploy/). tailnet/provider are emitted as data-point attributes (roadmap item L),
// so no resource-promotion shim is needed — they appear as real labels on every
// series directly; target_info is kept (exporter default). The translation
// strategy is pinned — see PromTranslationStrategy.
func newPrometheusReader(reg prometheus.Registerer) (sdkmetric.Reader, error) {
	return otelprom.New(
		otelprom.WithRegisterer(reg),
		otelprom.WithoutScopeInfo(),
		otelprom.WithTranslationStrategy(PromTranslationStrategy),
	)
}
