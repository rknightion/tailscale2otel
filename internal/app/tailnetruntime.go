package app

import (
	"log/slog"

	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v2/internal/audit"
	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/provider"
	"github.com/rknightion/tailscale2otel/v2/internal/rdns"
	"github.com/rknightion/tailscale2otel/v2/internal/release"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

// tailnetRuntime is the per-tailnet bundle the app fans out over: its own OTEL
// emitter (stamps tailscale.tailnet on every signal), cardinality tracker, control-plane
// provider+client, enrichment cache, registry+scheduler, status tracker, API
// stats, and poll-path flow/audit processors. Process-level singletons (admin
// server, checkpoint store, the webhook cross-dedup, runtime history) live on App.
type tailnetRuntime struct {
	name        string
	emitter     telemetry.Emitter
	card        *telemetry.CardinalityTracker
	exportStats func() telemetry.ExportStats
	cp          *provider.Provider
	client      *tsapi.Client // concrete Tailscale client; nil under provider: headscale
	cache       *enrich.DeviceCache
	registry    *collector.Registry
	sched       *collector.Scheduler
	status      *collector.StatusTracker
	apiStats    *APIStats
	flowProc    *flowlog.Processor
	auditProc   *audit.Processor
	flowDedup   *dedup.Set
	auditDedup  *dedup.Set
	nodeMetrics *nodemetrics.Collector // nil unless the node-metrics collector is enabled
	// Resolved per-tailnet identity for the status page (#116): in multi-tailnet
	// mode these come from the tailnets[] entry, NOT the unused top-level
	// tailscale: block. Empty authMethod ⇒ not a Tailscale runtime (headscale).
	authMethod     string
	apiKeySet      bool
	oauthSecretSet bool
}

// runtimeDeps carries the process-level dependencies a runtime needs at build
// time but does not own.
type runtimeDeps struct {
	cfg          *config.Config
	logger       *slog.Logger
	tracer       trace.Tracer
	store        collector.CheckpointStore
	procEmitter  telemetry.Emitter // for shared-infra self-obs (rdns)
	rdnsCache    *rdns.Cache       // shared external-address resolver; nil when disabled
	webhookDedup *dedup.Set        // single-tailnet webhook<->audit cross set; nil otherwise
	tsRelease    *release.Fetcher  // shared upstream-version fetcher; nil when disabled
	multi        bool              // true when >1 tailnet (enables checkpoint namespacing)
	primary      bool              // true for the first runtime; owns process-global static node_metrics targets (#59)
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
	rt.flowProc = flowlog.NewProcessor(rt.cache, fopts)

	rt.auditDedup = dedup.New(auditDedupCapacity)
	auditOpts := []audit.Option{audit.WithDedup(rt.auditDedup)}
	if d.webhookDedup != nil {
		auditOpts = append(auditOpts, audit.WithCrossDedup(d.webhookDedup))
	}
	rt.auditProc = audit.NewProcessor(auditOpts...)

	registerCollectors(rt, d)
	return rt
}
