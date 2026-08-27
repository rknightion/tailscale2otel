package app

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v4/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/dedup"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/eventstore"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v4/internal/geoip"
	"github.com/rknightion/tailscale2otel/v4/internal/k8saudit"
	"github.com/rknightion/tailscale2otel/v4/internal/provider"
	"github.com/rknightion/tailscale2otel/v4/internal/rdns"
	"github.com/rknightion/tailscale2otel/v4/internal/release"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// tailnetRuntime is the per-tailnet bundle the app fans out over: its own OTEL
// emitter (stamps tailscale.tailnet on every signal), cardinality tracker, control-plane
// provider+client, enrichment cache, registry+scheduler, status tracker, API
// stats, and poll-path flow/audit processors. Process-level singletons (admin
// server, checkpoint store, the webhook cross-dedup, runtime history) live on App.
type tailnetRuntime struct {
	configuredName string
	name           string
	emitter        telemetry.Emitter
	card           *telemetry.CardinalityTracker
	exportStats    func() telemetry.ExportStats
	forceFlush     func(context.Context) error
	cp             *provider.Provider
	client         *tsapi.Client // concrete Tailscale client; nil under provider: headscale
	cache          *enrich.DeviceCache
	registry       *collector.Registry
	sched          *collector.Scheduler
	status         *collector.StatusTracker
	apiStats       *APIStats
	// apiState records each API operation's latest availability state, and
	// coverage tallies per-entity subrequests. Both are per-tailnet: a scope
	// denial on one tailnet says nothing about another, and merging them would
	// let a healthy tailnet mask a broken one on the status page. In-process
	// introspection only — they emit no OTLP themselves (#420, #421, #430).
	apiState *apistate.Tracker
	coverage *apistate.Coverage
	flowProc *flowlog.Processor
	// flowStore backs the /flows view for THIS tailnet. One per runtime, not one
	// process-wide: a device name is unique only within its tailnet, so a shared
	// store would merge two different machines into one vertex of the topology.
	// Nil when the view is disabled or unreachable (see newFlowStore).
	//
	// Typed as the interface, not *flowstore.Memory, so the opt-in persistent
	// backend (#294) is a construction-time choice the handlers never see. Every
	// nil check below still holds: newFlowStore returns a literal nil on the
	// disabled path rather than a typed nil pointer, which would be a non-nil
	// interface and would turn "view disabled" into a nil-dereference at the
	// first request.
	flowStore flowstore.Store
	// flowStoreErr is why flowStore is nil despite the config asking for one.
	// Held per runtime so the status surface can name the tailnet and the
	// cause; a nil store with a nil error just means flows were not requested.
	flowStoreErr error
	// policy holds this tailnet's compiled ACL, fed by the acl and users
	// collectors and read by the flow processor. Nil when the flow view is off:
	// its only consumer is the reconciliation the view renders.
	policy       *aclpolicy.Store
	auditProc    *audit.Processor
	k8sAuditProc *k8saudit.Processor
	flowDedup    *dedup.Set
	auditDedup   *dedup.Set
	nodeMetrics  *nodemetrics.Collector // nil unless the node-metrics collector is enabled
	// Resolved per-tailnet identity for the status page (#116): in multi-tailnet
	// mode these come from the tailnets[] entry, NOT the unused top-level
	// tailscale: block. Empty authMethod ⇒ not a Tailscale runtime (headscale).
	authMethod     string
	apiKeySet      bool
	oauthSecretSet bool
}

// runtimeName is a runtime's display and lookup name. It is empty on the
// single-runtime assembly seam (and until New() resolves a "-" placeholder), so
// it falls back to the configured tailnet — the status page and the flow view
// must agree on what to call a tailnet, or a selector value stops matching.
func (a *App) runtimeName(rt *tailnetRuntime) string {
	if rt.name != "" {
		return rt.name
	}
	return a.cfg.Tailscale.Tailnet
}

func (a *App) runtimeConfiguredName(rt *tailnetRuntime) string {
	if rt.configuredName != "" {
		return rt.configuredName
	}
	if a.cfg.Provider == "headscale" {
		return "headscale"
	}
	return a.cfg.Tailscale.Tailnet
}

// runtimeDeps carries the process-level dependencies a runtime needs at build
// time but does not own.
type runtimeDeps struct {
	cfg           *config.Config
	logger        *slog.Logger
	tracer        trace.Tracer
	store         collector.CheckpointStore
	evidenceStore collector.CheckpointStore
	procEmitter   telemetry.Emitter  // for shared-infra self-obs (rdns)
	rdnsCache     *rdns.Cache        // shared external-address resolver; nil when disabled
	geoDB         *geoip.DB          // shared local GeoIP/ASN databases; nil when disabled
	eventStore    *eventstore.Memory // shared bounded audit/webhook event store (#300); nil when disabled
	webhookDedup  *dedup.Set         // single-tailnet webhook<->audit cross set; nil otherwise
	tsRelease     *release.Fetcher   // shared upstream-version fetcher; nil when disabled
	multi         bool               // true when >1 tailnet (enables checkpoint namespacing)
	primary       bool               // true for the first runtime; owns process-global static node_metrics targets (#59)
}

// newRuntime assembles a per-tailnet runtime: emitter/provider/client are already
// built (each tailnet has its own provider + auth); this wires the cache,
// processors, scheduler, and collectors. registerCollectors then populates the
// registry.
func newRuntime(rt *tailnetRuntime, d runtimeDeps) *tailnetRuntime {
	cfg := d.cfg
	selfObs := cfg.SelfObservability.Enabled

	rt.cache = enrich.NewDeviceCache()
	rt.status = collector.NewStatusTracker()
	rt.apiState = apistate.NewTracker()
	rt.coverage = apistate.NewCoverage()
	rt.registry = collector.NewRegistry()

	schedOpts := []collector.SchedulerOption{
		collector.WithLogger(withComponent(d.logger, compCollector)),
		collector.WithSelfObs(selfObs),
		collector.WithStatusTracker(rt.status),
		collector.WithTracer(d.tracer),
	}
	if d.multi {
		schedOpts = append(schedOpts, collector.WithCheckpointNamespace(rt.name))
	}
	rt.sched = collector.NewScheduler(rt.emitter, d.store, schedOpts...)

	// Poll-path processors: each tailnet has its own flow/audit processor bound to
	// its emitter + enrichment cache. The dedup sets suppress the inclusive-window
	// overlap (and, in single-tailnet, the poll<->stream cross-source overlap).
	rt.flowDedup = dedup.New(flowDedupCapacity)
	fopts := flowOptions(cfg)
	fopts.Dedup = rt.flowDedup
	if d.rdnsCache != nil {
		fopts.RDNS = d.rdnsCache
	}
	if d.geoDB != nil {
		fopts.Geo = d.geoDB
		// cardinality.flow.geo_dims governs the METRIC surface only; flow LOGS
		// carry the full geo/AS detail whenever a database is loaded.
		fopts.GeoDims = cfg.Cardinality.Flow.GeoDims
	}
	// The store is fed from the processor, so both ingestion paths (poll and the
	// stream receiver, which share this processor) populate the flow view.
	if rt.flowStore, rt.flowStoreErr = newFlowStore(cfg, rt.name, d.logger); rt.flowStore != nil {
		fopts.Store = rt.flowStore
		// No PII filtering on the way in: pii_filter governs the telemetry this
		// process exports, and the store is local, in-memory and readable only
		// through the admin-authenticated /flows surface (#241).
		//
		// That reasoning is about MEMORY, so it stops holding once rows are
		// written to a file. The persistent backend therefore applies pii_filter
		// itself, at its own write path (see newFlowStore's Redact option) rather
		// than here — filtering here would strip the in-memory view too and undo
		// #241 for everyone.
		//
		// Policy reconciliation rides along with the flow view; the acl and users
		// collectors fill this in as they run (see registerCollectors), and the
		// processor reads it per connection. Until the first ACL lands it holds no
		// policy, which the processor reads as "do not evaluate" rather than as
		// "nothing is permitted".
		rt.policy = &aclpolicy.Store{}
		fopts.Policy = rt.policy
	}
	rt.flowProc = flowlog.NewProcessor(rt.cache, fopts)

	rt.auditDedup = dedup.New(auditDedupCapacity)
	auditOpts := []audit.Option{
		audit.WithDedup(rt.auditDedup),
		audit.WithLogger(withComponent(d.logger, compCollector)),
	}
	changeStore := d.evidenceStore
	if d.multi {
		changeStore = collector.Namespaced(d.evidenceStore, rt.name)
	}
	auditOpts = append(auditOpts, audit.WithChangeCheckpointStore(changeStore))
	if d.webhookDedup != nil {
		auditOpts = append(auditOpts, audit.WithCrossDedup(d.webhookDedup))
	}
	if d.eventStore != nil {
		// Fed AFTER emission and only for events that survive both dedup gates,
		// so the local explorer can never delay or drop an OTLP record (#300).
		auditOpts = append(auditOpts, audit.WithStore(d.eventStore))
	}
	rt.auditProc = audit.NewProcessor(auditOpts...)

	// The Kubernetes-audit processor needs no dedup sets: tsrecorder writes one
	// object per record with no overlapping window, and the object-store engine's
	// own durable seen set already suppresses a re-listed object.
	rt.k8sAuditProc = k8saudit.NewProcessor(
		k8saudit.WithLogger(d.logger),
		// Exec command text follows the same opt-OUT rule as every other PII
		// category: exported unless the operator turns it off.
		k8saudit.WithEmitCommandText(d.cfg.PIIFilter.CommandText),
	)

	registerCollectors(rt, d)
	return rt
}
