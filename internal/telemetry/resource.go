package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry/pii"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// Bounds on #380's opt-in Resource enrichment. The metrics Resource's
// attributes are a per-series cost multiplier only via the promoted-namespace
// mechanism described in reservedPromotedLabels (Grafana Cloud promotes the
// whole service.* namespace) — everything else enrichment can add lands only
// in target_info. The caps below exist so a misconfigured or malicious
// OTEL_RESOURCE_ATTRIBUTES / attributes map cannot grow the Resource without
// bound: 32 custom attributes is generous for the intended use (a handful of
// deploy/ownership tags) while keeping the Resource small enough to log and
// diff by hand; 128/256 bytes for key/value comfortably covers real semconv
// keys and typical tag values while rejecting anything pathological (a
// dumped credential, a full stack trace) that would bloat every export.
const (
	maxResourceAttrKeyLen   = 128
	maxResourceAttrValueLen = 256
	maxResourceAttrCount    = 32
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
	// When the Hostnames category is explicitly disabled, omit WithHost() so that
	// host.name is never included in the Resource (it would otherwise be promoted
	// to target_info and leak the hostname to the backend). The same gate also
	// strips any host.name an operator tries to reintroduce via enrichment below.
	hostnamesOff := false
	if v, ok := opts.PIIFilter[pii.CatHostnames]; ok && !v {
		hostnamesOff = true
	}

	enrichment, err := enrichmentAttrs(ctx, opts, hostnamesOff)
	if err != nil {
		return nil, fmt.Errorf("resource enrichment: %w", err)
	}

	// The schemaless WithAttributes block carries the service.* identity; the core
	// detectors add host/os/process attributes so multiple instances are
	// distinguishable in Grafana. All detectors share one semconv schema URL, so
	// merging them with the schemaless block cannot raise a schema-URL conflict.
	// A narrow process subset is used deliberately — WithProcess() would also
	// emit process.command_args and process.owner, which can leak deploy paths
	// and usernames to the backend.
	//
	// Enrichment is applied FIRST, app identity SECOND: resource.New's merge
	// (auto.go's detect()) makes each later detector win any key collision
	// against earlier ones. enrichmentAttrs already filters out service.name /
	// service.instance.id / service.version / the tailscale.* signal-scoped keys
	// by name (see isReservedResourceKey), so this ordering is defense-in-depth,
	// not the only thing standing between an operator's config and the app's own
	// identity — "app identity always wins" holds even if that filter were ever
	// weakened.
	var detectors []resource.Option
	if len(enrichment) > 0 {
		detectors = append(detectors, resource.WithAttributes(enrichment...))
	}
	detectors = append(detectors,
		resource.WithAttributes(attrs...),
		resource.WithTelemetrySDK(),
		resource.WithOS(),
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
	)
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
//
// service_namespace joins this set once opts.Resource.ServiceNamespace is set
// and would actually be emitted (i.e. it also passed the enrichment length
// bound below) — #380 adds service.namespace to the metrics Resource, and
// Grafana Cloud promotes the whole service.* namespace, so a data-point
// attribute normalizing to service_namespace would now collide the same way
// service_name/service_instance_id already do.
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
	if ns := opts.Resource.ServiceNamespace; ns != "" && len(ns) <= maxResourceAttrValueLen {
		r["service_namespace"] = struct{}{}
	}
	return r
}

// ResourceOptions carries opt-in standard Resource enrichment (#380): a
// service namespace, a deployment environment, a bounded map of arbitrary
// custom attributes, and controlled OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME
// support. A zero value must produce byte-identical Resources to today's,
// including the deliberate omission of service.version from the metrics
// Resource (#187) — see TestResourceEnrichmentZeroValueByteIdentical.
//
// Every value that reaches the Resource through this struct passes through
// enrichmentAttrs, which enforces (in this order): app identity always wins
// (service.name/service.instance.id/service.version, and the tailnet/provider
// signal-scoped keys, can never be set here — see isReservedResourceKey), the
// pii.CatHostnames gate (host.name stays out even if enrichment tries to add
// it back), and the maxResourceAttrKeyLen/maxResourceAttrValueLen/
// maxResourceAttrCount bounds (oversized or excess attributes are dropped with
// a warning via Options.Logger, never a fatal error — consistent with the
// rest of this file's fail-open partial-resource handling).
type ResourceOptions struct {
	// ServiceNamespace becomes the service.namespace Resource attribute
	// (metrics AND logs/traces). Grafana Cloud promotes it to a `job`-adjacent
	// label alongside service.name, so it is deliberately a first-class field
	// (bounded, reserved-label-aware — see reservedPromotedLabels) rather than
	// just another entry in Attributes.
	ServiceNamespace string

	// DeploymentEnvironment becomes the deployment.environment.name Resource
	// attribute (current stable semconv name; the OTEL SDK pinned here,
	// v1.44.0, still ships the now-deprecated deployment.environment key on
	// its own detectors, but a new field should not standardize on a
	// deprecated name). It is NOT under the service.* namespace, so Grafana
	// Cloud does not promote it to a per-series label — it lands in
	// target_info only, same as every other non-service.* resource attribute.
	DeploymentEnvironment string

	// Attributes is a bounded map of arbitrary custom Resource attributes
	// (e.g. team ownership tags, deploy metadata). Keys are processed in
	// sorted order for deterministic drop behavior once maxResourceAttrCount
	// is reached. A key or value exceeding the length bounds, or a key that is
	// reserved (see isReservedResourceKey) or gated by pii.CatHostnames, is
	// dropped with a warning rather than admitted or treated as fatal.
	Attributes map[string]string

	// FromEnv opts into resource.WithFromEnv() (OTEL_RESOURCE_ATTRIBUTES +
	// OTEL_SERVICE_NAME), filtered by the exact same guards as Attributes.
	// Default false: resource.WithFromEnv() gives an operator's ambient
	// process environment unbounded reach over what is, on the metrics
	// Resource, a per-series label surface — this must be an explicit,
	// reviewed opt-in, not inherited for free.
	FromEnv bool
}

// isReservedResourceKey reports whether key may never be set through
// enrichment (Attributes or FromEnv), regardless of its value: the whole
// service.* namespace is reserved for the app's own identity
// (service.name/service.instance.id/service.version) plus the two dedicated,
// separately-bounded fields (ServiceNamespace, DeploymentEnvironment) — so an
// operator wanting a namespace uses the field, not a bypass through the map —
// and the tailscale.*/tailscale2otel.provider keys are reserved because they
// are signal-scoped const attributes (constLabelAttrs), never Resource
// attributes (roadmap item L).
func isReservedResourceKey(key string) bool {
	switch key {
	case semconv.AttrTailnet, semconv.AttrProvider:
		return true
	}
	return strings.HasPrefix(key, "service.")
}

// enrichmentAttrs builds the filtered, bounded list of attribute.KeyValue to
// merge onto the Resource ahead of the app's own identity attrs (see the
// ordering comment in buildResource). It never returns a key that
// isReservedResourceKey rejects, never host.name when hostnamesOff, and never
// more than maxResourceAttrCount entries total across Attributes and (if
// opted in) FromEnv — Attributes is considered first, so FromEnv only fills
// whatever budget Attributes left. Dropped entries are logged via
// opts.Logger (nil is a silent no-op logger, matching the rest of this
// package) rather than failing buildResource — a misconfigured custom
// attribute must not take down telemetry startup.
func enrichmentAttrs(ctx context.Context, opts Options, hostnamesOff bool) ([]attribute.KeyValue, error) {
	ro := opts.Resource
	var out []attribute.KeyValue
	budget := maxResourceAttrCount

	warnf := func(format string, args ...any) {
		if opts.Logger != nil {
			opts.Logger.Warn(fmt.Sprintf(format, args...))
		}
	}

	addField := func(key, val string) {
		if val == "" {
			return
		}
		if len(val) > maxResourceAttrValueLen {
			warnf("resource enrichment: %s value exceeds %d bytes, dropping", key, maxResourceAttrValueLen)
			return
		}
		out = append(out, attribute.String(key, val))
	}
	addField("service.namespace", ro.ServiceNamespace)
	addField("deployment.environment.name", ro.DeploymentEnvironment)

	addCandidate := func(key, val, source string) {
		if isReservedResourceKey(key) {
			warnf("resource enrichment: %s attribute %q is reserved (app identity or a signal-scoped attribute), dropping", source, key)
			return
		}
		if hostnamesOff && key == "host.name" {
			warnf("resource enrichment: %s attribute %q dropped by the hostnames PII gate", source, key)
			return
		}
		if len(key) > maxResourceAttrKeyLen {
			warnf("resource enrichment: %s attribute key %q exceeds %d bytes, dropping", source, key, maxResourceAttrKeyLen)
			return
		}
		if len(val) > maxResourceAttrValueLen {
			warnf("resource enrichment: %s attribute %q value exceeds %d bytes, dropping", source, key, maxResourceAttrValueLen)
			return
		}
		if budget <= 0 {
			warnf("resource enrichment: attribute budget (%d) exhausted, dropping %s attribute %q", maxResourceAttrCount, source, key)
			return
		}
		out = append(out, attribute.String(key, val))
		budget--
	}

	if len(ro.Attributes) > 0 {
		keys := make([]string, 0, len(ro.Attributes))
		for k := range ro.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			addCandidate(k, ro.Attributes[k], "attributes")
		}
	}

	if ro.FromEnv {
		envAttrs, err := envResourceAttrs(ctx)
		if err != nil {
			return nil, err
		}
		for _, kv := range envAttrs {
			addCandidate(string(kv.Key), kv.Value.AsString(), "from_env")
		}
	}

	return out, nil
}

// envResourceAttrs runs the real SDK resource.WithFromEnv() detector in
// isolation (parsing OTEL_RESOURCE_ATTRIBUTES + OTEL_SERVICE_NAME) and returns
// its attributes for enrichmentAttrs to filter — using the SDK's own env
// parsing (percent-decoding, comma-splitting) rather than reimplementing it.
// A partial result (e.g. a malformed pair in OTEL_RESOURCE_ATTRIBUTES) is
// non-fatal, consistent with buildResource's own ErrPartialResource handling.
func envResourceAttrs(ctx context.Context) ([]attribute.KeyValue, error) {
	r, err := resource.New(ctx, resource.WithFromEnv())
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return r.Attributes(), nil
}
