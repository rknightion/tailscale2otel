package telemetry

import (
	"context"
	"errors"

	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry/pii"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// OTEL Resource construction and the provider-scoped constant attributes that
// deliberately are NOT on the Resource. Split out of provider.go so Resource
// policy lives in one file with one owner.

// buildResource builds the OTEL resource. includeServiceVersion controls whether
// service.version is attached: it is TRUE for the logs/traces resource and FALSE
// for the metrics resource.
//
// The split exists because the OTLP->Prometheus convention promotes only
// service.name (+service.namespace)->job and service.instance.id->instance to
// labels; every other resource attribute belongs on the target_info info metric.
// Grafana Cloud's OTLP ingest deviates and promotes the whole service.* namespace,
// so a service.version on the metrics resource becomes a service_version label on
// EVERY series. That makes each build mint a fresh series set: after a redeploy the
// old and new versions' series coexist for the query lookback window (an OTLP push
// carries no staleness signal, unlike a scrape target going down), so any panel
// that sums across a bounded dimension transiently doubles — and active-series
// cardinality grows by the number of versions ever seen (#187; the doubling was
// diagnosed live in graph2otel#104, which runs a per-commit :main build).
//
// Version stays queryable from metrics via the tailscale2otel.build_info gauge
// (join with group_left). Logs and traces are never summed and have no per-series
// label surface, so their resource keeps service.version for per-record/-span
// version attribution.
func buildResource(ctx context.Context, opts Options, includeServiceVersion bool) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{attribute.String("service.name", opts.ServiceName)}
	if includeServiceVersion && opts.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", opts.ServiceVersion))
	}
	if opts.InstanceID != "" {
		attrs = append(attrs, attribute.String("service.instance.id", opts.InstanceID))
	}
	// The schemaless WithAttributes block carries the service.* identity; the core
	// detectors add host/os/process attributes so multiple instances are
	// distinguishable in Grafana. All detectors share one semconv schema URL, so
	// merging them with the schemaless block cannot raise a schema-URL conflict.
	// A narrow process subset is used deliberately — WithProcess() would also
	// emit process.command_args and process.owner, which can leak deploy paths
	// and usernames to the backend.
	detectors := []resource.Option{
		resource.WithAttributes(attrs...),
		resource.WithTelemetrySDK(),
		resource.WithOS(),
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
	}
	// When the Hostnames category is explicitly disabled, omit WithHost() so that
	// host.name is never included in the Resource (it would otherwise be promoted
	// to target_info and leak the hostname to the backend).
	hostnamesOff := false
	if v, ok := opts.PIIFilter[pii.CatHostnames]; ok && !v {
		hostnamesOff = true
	}
	if !hostnamesOff {
		detectors = append(detectors, resource.WithHost())
	}
	res, err := resource.New(ctx, detectors...)
	// A partial resource (a detector that couldn't read its source — e.g.
	// os.Hostname() failing) must NOT abort startup: the exporter's core job is
	// unaffected, so continue with whatever attributes were resolved. Any other
	// error (which, given the shared schema URL, should not occur) is fatal.
	if err != nil && errors.Is(err, resource.ErrPartialResource) {
		return res, nil
	}
	return res, err
}

// constLabelAttrs returns the provider-scoped attributes stamped onto every signal
// (metric data point, log record, span) for a provider built from opts: the
// tailnet name and control-plane provider, each included only when non-empty.
// Roadmap item L moved these off the Resource so they are real, joinless labels on
// every backend (Grafana Cloud, the Prometheus pull endpoint, self-managed Mimir).
//
// PII gate: when opts.PIIFilter explicitly disables pii.CatTailnetName (i.e. the
// category is present in the map and set to false), the tailscale.tailnet attribute
// is omitted. In multi-tailnet mode this removes the per-tailnet label from all
// signals for that provider; per-tailnet series still remain distinct via the
// service.instance.id resource attribute. Category absent from the map, or present
// and true, behaves as today (attribute emitted). The tailscale2otel.provider
// attribute is NOT PII and is always included when non-empty.
func constLabelAttrs(opts Options) []attribute.KeyValue {
	var out []attribute.KeyValue
	if opts.TailnetName != "" {
		// Omit tailscale.tailnet when the operator has explicitly disabled the
		// tailnet_name PII category (same gate pattern as buildResource/hostnames).
		if v, ok := opts.PIIFilter[pii.CatTailnetName]; !ok || v {
			out = append(out, attribute.String(semconv.AttrTailnet, opts.TailnetName))
		}
	}
	if opts.Provider != "" {
		out = append(out, attribute.String(semconv.AttrProvider, opts.Provider))
	}
	return out
}

// reservedPromotedLabels returns the Prometheus label names that Grafana Cloud
// promotes from the OTEL *metrics* Resource onto every exported series:
// service.name→job, service.instance.id→instance, plus the service_* labels
// (confirmed on live series). A data-point attribute that normalizes to one of
// these would duplicate the promoted label and get the whole sample rejected as
// otlp_parse_error, so the Emitter drops it (the resource value wins). Host/OS/
// process resource attributes are deliberately NOT reserved — Grafana keeps those
// in target_info only, so a data-point host.name (e.g. the node-metrics
// passthrough) does not collide.
//
// service_version is deliberately NOT reserved: the metrics resource no longer
// carries service.version (#187), so there is nothing for Grafana Cloud to promote
// and nothing to collide with. It stays on the logs/traces resource, which has no
// per-series label surface and so never reaches this guard.
func reservedPromotedLabels(opts Options) map[string]struct{} {
	r := map[string]struct{}{
		"job":      {},
		"instance": {},
	}
	if opts.ServiceName != "" {
		r["service_name"] = struct{}{}
	}
	if opts.InstanceID != "" {
		r["service_instance_id"] = struct{}{}
	}
	return r
}
