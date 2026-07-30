// Package app wires configuration, telemetry, the Tailscale client, the device
// cache, and the collector scheduler into a runnable service.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/grafana/pyroscope-go"
	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v4/internal/annotations"
	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/dedup"
	"github.com/rknightion/tailscale2otel/v4/internal/eventstore"
	"github.com/rknightion/tailscale2otel/v4/internal/geoip"
	"github.com/rknightion/tailscale2otel/v4/internal/hsapi"
	"github.com/rknightion/tailscale2otel/v4/internal/provider"
	"github.com/rknightion/tailscale2otel/v4/internal/rdns"
	"github.com/rknightion/tailscale2otel/v4/internal/release"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

const heartbeatInterval = 15 * time.Second

// Dedup-set capacities for the shared cross-source de-duplication carried by the
// flow and audit processors. They bound memory while comfortably covering the
// overlap window between the poll collectors and the streaming receiver (which
// share one processor each). Flow windows are higher-volume than audit events.
const (
	flowDedupCapacity  = 16384
	auditDedupCapacity = 4096
)

// autoConfigureTimeout bounds the optional startup log-stream registration so a
// slow/hung Tailscale endpoint cannot delay shutdown indefinitely.
const autoConfigureTimeout = 30 * time.Second

// App is the assembled, runnable service.
type App struct {
	cfg       *config.Config
	version   string       // injected build version, for the status page
	startTime time.Time    // process start, for uptime on the status page
	tracer    trace.Tracer // no-op when tracing.enabled=false; threads into scheduler+receivers

	// runtimes is the per-tailnet collection machinery (one element per tailnet,
	// always >=1). Each owns its emitter (stamps tailscale.tailnet on every signal),
	// provider/client, cache, registry+scheduler, status tracker, API stats, and
	// poll-path processors.
	runtimes []*tailnetRuntime

	// Process-level self-observability: the process provider carries process/
	// global signals (no tailnet dimension). Per-tailnet self-obs lives on each
	// runtime's emitter/card/exportStats.
	procEmitter telemetry.Emitter
	procCard    *telemetry.CardinalityTracker // process provider's tracker; nil when self-obs off
	// annotator publishes curated events to Grafana as annotations (#518). Nil
	// unless grafana_annotations.url is set, and a nil *Annotator is a working
	// no-op at every call site, so nothing branches on the feature being off.
	annotator *annotations.Annotator
	// batchQueues are the log/span processor queue trackers, one per provider,
	// paired with the emitter that must report them so each tailnet's queue
	// saturation is attributed to that tailnet. Empty when self-obs is off.
	batchQueues []batchQueueReport
	// credReload owns the outbound credential/TLS file watchers (#362). Nil-safe:
	// every method tolerates a nil receiver, and it stays nil when no watched file
	// is configured.
	credReload      *credReloaders
	procExportStats func() telemetry.ExportStats // process provider's export volume; nil when self-obs off
	// delivery reports what the OTLP exporters actually shipped, per signal,
	// across every provider. Populated regardless of self-obs (#317).
	delivery     func() []telemetry.DeliveryState
	metricGroups map[string]string // metric source-name -> catalog group, for series.by_group rollup

	shutdown    func(context.Context) error // flushes telemetry on stop
	restore     func()                      // restores the prior otel error handler
	runtimeHist *runtimeHistory             // short-term runtime/cardinality trends, for the status page
	store       collector.CheckpointStore   // checkpoint store; read for window-collector state on the status page
	// checkpointEffective is the store kind actually in use ("file"|"memory"),
	// which can differ from cfg.Checkpoint.Store after a fallback (unwritable path
	// or a corrupt file). The status page and the checkpoint reporter use this, not
	// the raw config value (#69).
	checkpointEffective string
	// checkpointPath is the file actually in use (empty for the memory store) and
	// checkpointReason explains any divergence from the config — an unwritable
	// path, a relocation to the platform state directory (#336), or a corrupt
	// file renamed aside. Both are surfaced on the status page and in
	// /api/status.json so an operator can see WHERE state went and WHY without
	// reading startup logs.
	checkpointPath   string
	checkpointReason string
	logger           *slog.Logger

	flowDedup     *dedup.Set // runtimes[0] flow set, retained for the dedup self-obs reporter
	auditDedup    *dedup.Set // runtimes[0] audit set, retained for the dedup self-obs reporter
	streamSrv     receiver
	webhookSrv    receiver
	ingressWAL    *ingressWALCoordinator
	webhookDedup  *dedup.Set            // shared cross-source set (webhook<->audit); nil unless enabled
	webhookDedups map[string]*dedup.Set // per-tailnet route sets in multi-tailnet mode
	adminSrv      *http.Server
	metricsSrv    *http.Server // prometheus pull endpoint; nil unless prometheus.enabled
	// Cert reloaders for the two listeners above, nil unless that listener
	// serves TLS. Held here rather than in a package-level registry keyed by
	// server pointer: a global would never drop an entry, and the lifetime of
	// a reloader is exactly the lifetime of its App (#316).
	adminCerts   *CertReloader
	metricsCerts *CertReloader
	profiler     *pyroscope.Profiler // pyroscope push profiler; nil unless enabled
	rdnsCache    *rdns.Cache         // async reverse-DNS cache; nil unless enrichment.reverse_dns.enabled
	// geoDB holds the local MaxMind databases; nil unless enrichment.geoip.enabled.
	// Like rdnsCache it is process-wide rather than per-tailnet: geography is a
	// property of an address, not of the tailnet that observed it.
	geoDB      *geoip.DB
	geoUpdater *geoip.Updater // downloads/reloads geoDB; nil when geoDB is
	// eventStore backs the /events explorer (#300). Unlike flowStore this is ONE
	// store for the whole process, not one per tailnet: an audit or webhook event
	// is already globally identified by its actor and target, so a shared store
	// gives the unified timeline the explorer is for, where per-tailnet stores
	// would force the operator to check each tailnet separately for a change that
	// may only have happened once. Nil when the explorer is disabled.
	eventStore *eventstore.Memory

	selfRelease *release.Fetcher // nil unless version_checks.self.enabled
	tsRelease   *release.Fetcher // nil unless version_checks.devices.enabled

	// readyState is the ONE source of component-failure state behind /readyz and
	// the status page: stream/webhook receivers (#57) plus the admin and
	// Prometheus listeners (#306). Written by recordComponentStop, read by the
	// readyz handler.
	readyState *componentHealth
}

// receiver is the small common surface shared by legacy single-tailnet servers
// and the multi-tailnet routers. Keeping it local prevents routing details from
// leaking into the app lifecycle.
type receiver interface {
	Handler() http.Handler
	Run(context.Context) error
}

// New assembles the service from cfg. The caller owns ctx for the lifetime of
// construction; Run takes its own ctx.
// Subsystem names used to tag each component's logger (semconv.AttrComponent),
// so operational logs are filterable per-subsystem (e.g. component=tsapi). The
// stream/webhook receivers reuse the appcatalog.Component* names that also label
// the component-error metric.
const (
	compTelemetry   = "telemetry"
	compTSAPI       = "tsapi"
	compCollector   = "collector"
	compCheckpoint  = "checkpoint"
	compProfiling   = "profiling"
	compNodeMetrics = "nodemetrics"
	compRelease     = "release"
	compGeoIP       = "geoip"
	compAnnotations = "annotations"
)

// withComponent returns a logger that tags every record with its subsystem name.
func withComponent(l *slog.Logger, component string) *slog.Logger {
	return l.With(semconv.AttrComponent, component)
}

// Option customizes New's construction beyond what cfg drives. The zero value
// (no options) is New's historical behavior exactly; today the only option is
// WithTelemetryOverride, used solely by -preflight (see
// internal/app/preflight.go, issue #311) to keep its normal-mode callers
// (runServer, and every existing caller of New before this option existed)
// completely unaffected.
type Option func(*newSettings)

type newSettings struct {
	telemetryOverride func(telemetry.Options) telemetry.Options
}

// WithTelemetryOverride rewrites the telemetry.Options New() would otherwise
// build from cfg via telemetryOptions, immediately before constructing the
// ProviderSet. -preflight (without -preflight-export) uses this to force
// Protocol="stdout" with a discarding StdoutWriter, so New()'s normal wiring
// never dials the operator's configured OTLP backend — there is no
// config-level way to express "build the exporters but discard their output",
// since Options.StdoutWriter has no cfg field of its own.
func WithTelemetryOverride(f func(telemetry.Options) telemetry.Options) Option {
	return func(s *newSettings) { s.telemetryOverride = f }
}

func New(ctx context.Context, cfg *config.Config, version string, logger *slog.Logger, opts ...Option) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	settings := &newSettings{}
	for _, o := range opts {
		o(settings)
	}
	resolved := cfg.ResolvedTailnets()
	multi := len(resolved) > 1

	// Telemetry labels default to the configured tailnet name. For the single "-"
	// placeholder (the "use my default tailnet" sentinel), best-effort resolve the
	// real tailnet name for the LABEL only — buildTailscaleProvider still gets the
	// unmodified rt, so the API path stays "-" and a failed/stale resolution can
	// never break polling.
	labels := make([]string, len(resolved))
	for i, rt := range resolved {
		labels[i] = rt.Name
	}
	if !multi && cfg.Provider != "headscale" && len(resolved) == 1 && resolved[0].Name == "-" {
		if rc, rcErr := newResolverClient(resolved[0], version, logger); rcErr == nil {
			if name := resolveTailnetName(ctx, rc, time.Now(), withComponent(logger, compTSAPI)); name != "" {
				labels[0] = name
			}
		}
	}

	base := telemetryOptions(cfg, version)
	// Outbound credential/TLS rotation (#362). Built before the providers exist so
	// the very first export already reads through the reloader rather than a
	// snapshot taken at construction, and before the override hook so a test can
	// still replace the dynamic funcs.
	reloaders, err := newCredReloaders(cfg, logger)
	if err != nil {
		return nil, err
	}
	applyDynamicOTLPCredentials(&base, reloaders.otlp, base.Headers, cfg.OTLP.GrafanaCloud.InstanceID)
	if settings.telemetryOverride != nil {
		base = settings.telemetryOverride(base)
	}
	base.Logger = withComponent(logger, compTelemetry) // surfaces Emitter label-collision diagnostics
	perTN := make([]telemetry.PerTailnetOptions, len(resolved))
	for i := range resolved {
		perTN[i] = telemetry.PerTailnetOptions{
			Name:       labels[i],
			InstanceID: instanceFor(base.InstanceID, labels[i], multi, cfg.PIIFilter.TailnetName),
		}
	}
	ps, err := telemetry.NewProviderSet(ctx, base, perTN)
	if err != nil {
		return nil, err
	}
	store, checkpointOut, err := checkpointStore(cfg, withComponent(logger, compCheckpoint))
	if err != nil {
		_ = ps.Shutdown(ctx)
		return nil, err
	}

	a := newAppShell(cfg, version, logger, ps.Process().Emitter(), ps.Process().Tracer(), ps.Shutdown, store)
	a.credReload = reloaders
	a.checkpointEffective = checkpointOut.Kind
	a.checkpointPath = checkpointOut.Path
	a.checkpointReason = checkpointOut.Reason
	a.procCard = ps.Process().Cardinality()
	if q := ps.Process().BatchQueues(); q != nil {
		a.batchQueues = append(a.batchQueues, batchQueueReport{emitter: a.procEmitter, tracker: q})
	}
	a.procExportStats = ps.Process().ExportStats
	a.delivery = ps.Delivery
	a.metricGroups = metricGroupMap()
	a.buildProcessDeps()

	// The annotation writer is built HERE, and the ordering is not arbitrary:
	//
	//   - AFTER the checkpoint store, because the persisted dedupe set lives
	//     beside it and shares its volume. Without one, every restart could
	//     republish what is still inside the collectors' overlap windows.
	//   - BEFORE any runtime, because a token that cannot write must be reported
	//     at startup rather than at the first real event, and because a collector
	//     that starts before the recorder exists emits records no rule ever sees.
	//
	// A configured-but-unwritable token is FATAL. See annotations.Start.
	if err := a.startAnnotator(ctx, cfg, version); err != nil {
		_ = ps.Shutdown(ctx)
		return nil, err
	}

	constructionComplete := false
	defer func() {
		if constructionComplete {
			return
		}
		cleanupFailedConstruction(
			ctx,
			func() {
				if a.ingressWAL != nil {
					_ = a.ingressWAL.Close()
				}
			},
			func() {
				if a.rdnsCache != nil {
					a.rdnsCache.Close()
				}
			},
			func() {
				if a.restore != nil {
					a.restore()
				}
			},
			func() {
				// Already published the startup marker by this point, so the
				// worker goroutine and its state file need closing even though
				// construction is being abandoned.
				_ = a.annotator.Close(ctx)
			},
			ps.Shutdown,
		)
	}()

	// Build one runtime per tailnet (Tailscale), or a single Headscale runtime.
	if cfg.Provider == "headscale" {
		// Headscale API requests were dark: untraced, and absent from both the
		// api.* self-obs metrics and the status page's API panel, while the
		// equivalent Tailscale calls had all three (#371). Give it the same
		// treatment. hsapi has no retry and no client-side rate limiter, so
		// Attempts is always 1 and WaitDuration always 0 — stated here rather
		// than left as two silently-zero columns.
		hsAPIStats := NewAPIStats()
		var hsObs func(context.Context, string, int, int, time.Duration, time.Duration)
		if cfg.SelfObservability.Enabled {
			hsObs = apiObserver(a.procEmitter)
		}
		hsOpts := hsapiOptions(cfg, a.tracer)
		hsOpts.OnRequest = func(ctx context.Context, i hsapi.RequestInfo) {
			if hsObs != nil {
				hsObs(ctx, i.Endpoint, i.Status, 1, i.Duration, 0)
			}
			hsAPIStats.Record(tsapi.RequestInfo{
				Endpoint: i.Endpoint,
				Status:   i.Status,
				Attempts: 1,
				Duration: i.Duration,
				Err:      i.Err,
			})
		}
		hsClient := hsapi.NewClient(hsOpts)
		cp := provider.Headscale(hsapi.NewProvider(hsClient))
		// Headscale has no tailnet fan-out: collect under the process provider's
		// emitter (no tailscale.tailnet attribute), matching v1 single-Resource output.
		// Pass nil card/exportStats (not the process provider's) so the per-runtime
		// self-obs reporters and the status-page snapshot/total do NOT double-count:
		// the headscale runtime shares the process emitter, so its self-obs
		// (export.*, series.*) is already covered exactly once by the process-level
		// reporters — aliasing them here ran a second reporter pair over identical
		// state, inflating export.* ~2x and corrupting series.active (#54). This
		// mirrors the newApp test seam and the multi-tailnet design (distinct
		// providers get their own trackers; a shared provider gets none).
		hsRuntime := a.addRuntimeConfigured(
			"headscale",
			"",
			a.annotator.Decorate("headscale", a.procEmitter),
			nil,
			nil,
			ps.Process().ForceFlush,
			cp,
			multi,
		)
		// addRuntimeConfigured mints its own APIStats, but the request hook above
		// had to be closed over one that existed before the client was built.
		// Swap in the one actually being written to, or the status page's API
		// panel reads an object nothing ever records into.
		hsRuntime.apiStats = hsAPIStats
	} else {
		for i, rt := range resolved {
			label := labels[i]
			tp := ps.Tailnet(label)
			// Teed so every record this tailnet's collectors emit is offered to
			// the annotation rule set. The tee forwards everything unchanged and
			// is a pass-through when the writer is off.
			emitter := a.annotator.Decorate(rt.Name, tp.Emitter())
			// Each tailnet provider reports its own queue tracker through its own
			// emitter, so tailscale.tailnet rides the saturation series.
			if q := tp.BatchQueues(); q != nil {
				a.batchQueues = append(a.batchQueues, batchQueueReport{emitter: emitter, tracker: q})
			}
			apiStats := NewAPIStats()
			cp, err := buildTailscaleProvider(rt, version, logger, a.tracer, emitter, apiStats, cfg.SelfObservability.Enabled)
			if err != nil {
				// Attribute the failure to the offending tailnet so an MSP with many
				// entries knows which one to fix (e.g. a mis-mounted secret) instead of
				// a bare "no authentication configured" — #125.
				return nil, fmt.Errorf("tailnets[%d] %q: %w", i, label, err)
			}
			r := a.addRuntimeConfigured(
				rt.Name,
				label,
				emitter,
				tp.Cardinality(),
				tp.ExportStats,
				tp.ForceFlush,
				cp,
				multi,
			)
			r.apiStats = apiStats
			// Resolved per-tailnet identity for the status page (#116) — from the
			// tailnets[] entry (rt is the ResolvedTailnet), not the top-level block.
			r.authMethod = rt.Auth.Method
			r.apiKeySet = rt.Auth.APIKey != ""
			r.oauthSecretSet = rt.Auth.OAuth.ClientSecret != ""
		}
	}

	if cfg.SelfObservability.Enabled {
		a.restore = telemetry.InstallExportErrorHandler(a.procEmitter, withComponent(logger, compTelemetry))
		telemetry.EmitBuildInfo(a.procEmitter, version, runtime.Version())
	}
	if len(a.runtimes) > 0 {
		a.flowDedup = a.runtimes[0].flowDedup
		a.auditDedup = a.runtimes[0].auditDedup
	}
	// Reconcile checkpoint keys with the current namespacing shape (single<->multi
	// transitions, tailnet renames) so window cursors survive instead of silently
	// cold-starting and re-emitting the overlap window (#105).
	a.migrateCheckpointKeys(withComponent(logger, compCheckpoint))
	ingressRoutes := a.buildReceivers()
	if err := a.buildIngressWAL(ingressRoutes); err != nil {
		return nil, fmt.Errorf("ingress WAL: %w", err)
	}
	if cfg.Admin.Enabled {
		a.adminSrv = a.buildAdminServer()
	}
	if cfg.Prometheus.Enabled {
		if g := ps.PromGatherer(); g != nil {
			a.metricsSrv = a.buildMetricsServer(g)
		}
	}

	// Continuous profiling is opt-in. startProfiling also applies the runtime
	// mutex/block sampling rates needed by the /debug/pprof pull path. A failure
	// to reach Pyroscope is non-fatal: the exporter's core job is unaffected.
	// The emitter turns on the profiling.upload.* self-obs metrics (#374). The
	// health tracker records regardless — the admin status page must work on a
	// deployment that exports no self-telemetry at all — so this only controls
	// whether that state also leaves the process as metrics.
	profLogger := withComponent(logger, compProfiling)
	prof, err := startProfiling(cfg, version, profLogger, withProfilingEmitter(a.procEmitter), withProfilingCredReload(a.credReload.pyroscopeReloader()))
	if err != nil {
		profLogger.Error("pyroscope profiler failed to start", "error", err)
	}
	a.profiler = prof
	constructionComplete = true
	return a, nil
}

func cleanupFailedConstruction(
	ctx context.Context,
	closeWAL func(),
	closeRDNS func(),
	restoreTelemetry func(),
	closeAnnotator func(),
	shutdownTelemetry func(context.Context) error,
) {
	if closeWAL != nil {
		closeWAL()
	}
	if closeRDNS != nil {
		closeRDNS()
	}
	if restoreTelemetry != nil {
		restoreTelemetry()
	}
	if closeAnnotator != nil {
		closeAnnotator()
	}
	if shutdownTelemetry != nil {
		_ = shutdownTelemetry(ctx)
	}
}

// buildTailscaleProvider constructs an instrumented Tailscale provider for one
// resolved tailnet: its own auth + the combined request hook (APIStats always
// records for the status page; apiObserver emits OTLP only with self-obs on).
func buildTailscaleProvider(
	rt config.ResolvedTailnet,
	version string,
	logger *slog.Logger,
	tracer trace.Tracer,
	emitter telemetry.Emitter,
	apiStats *APIStats,
	selfObs bool,
) (*provider.Provider, error) {
	tsOpts := tsapiOptionsFor(rt, version)
	tsOpts.Logger = withComponent(logger, compTSAPI)
	tsOpts.Tracer = tracer
	var obs func(context.Context, string, int, int, time.Duration, time.Duration)
	if selfObs {
		obs = apiObserver(emitter)
	}
	tsOpts.OnRequest = func(ctx context.Context, i tsapi.RequestInfo) {
		if obs != nil {
			obs(ctx, i.Endpoint, i.Status, i.Attempts, i.Duration, i.WaitDuration)
		}
		apiStats.Record(i)
	}
	client, err := tsapi.NewClient(tsOpts)
	if err != nil {
		return nil, err
	}
	return provider.Tailscale(client), nil
}

// newAppShell builds an App with only its process-level fields set; runtimes are
// added separately via addRuntime.
func newAppShell(
	cfg *config.Config,
	version string,
	logger *slog.Logger,
	procEmitter telemetry.Emitter,
	tracer trace.Tracer,
	shutdown func(context.Context) error,
	store collector.CheckpointStore,
) *App {
	if logger == nil {
		logger = slog.Default()
	}
	return &App{
		cfg:         cfg,
		version:     version,
		startTime:   time.Now(),
		tracer:      tracer,
		procEmitter: procEmitter,
		shutdown:    shutdown,
		runtimeHist: newRuntimeHistory(runtimeHistoryLen),
		store:       store,
		logger:      logger,
		readyState:  newComponentHealth(),
	}
}

// buildProcessDeps constructs the process-level shared dependencies that some
// runtimes consume at build time: the version-check fetchers, the shared
// reverse-DNS cache, and the webhook<->audit cross-dedup set. Must be called
// before addRuntime (the devices collector wants a.tsRelease; runtimes[0] wants
// the rdns cache + webhook dedup).
func (a *App) buildProcessDeps() {
	cfg := a.cfg
	if cfg.Enrichment.ReverseDNS.Enabled {
		ropts := rdnsOptions(cfg)
		// rdns is shared infra across tailnets; its self-obs rides the process
		// provider. The status page reads Stats() directly regardless.
		if cfg.SelfObservability.Enabled {
			ropts.Emitter = a.procEmitter
		}
		a.rdnsCache = rdns.New(ropts)
	}
	a.buildGeoIP()
	// One store shared by every tailnet's audit processor and every webhook
	// route; see the field comment for why this is not per-runtime.
	a.eventStore = newEventStore(cfg)
	if cfg.Webhook.Enabled && cfg.Webhook.DedupAuditEvents && len(cfg.Webhook.Routes) == 0 {
		// Best-effort cross-SOURCE de-dup so a change reported by BOTH a webhook and
		// the audit logs is counted once (single-tailnet only; webhook requires it).
		a.webhookDedup = dedup.New(auditDedupCapacity)
	}
	if cfg.Webhook.Enabled && cfg.Webhook.DedupAuditEvents && len(cfg.Webhook.Routes) > 0 {
		a.webhookDedups = make(map[string]*dedup.Set, len(cfg.Webhook.Routes))
	}
	vc := cfg.VersionChecks
	ua := "tailscale2otel/" + a.version
	releaseLogger := withComponent(a.logger, compRelease)
	if vc.Self.Enabled {
		a.selfRelease = release.NewFetcher("self", release.GitHubLatestURL, ua,
			release.ParseGitHubLatest, newReleaseHTTPClient(vc.Timeout.D()),
			vc.CacheTTL.D(), releaseLogger, release.WithTracer(a.tracer))
	}
	if vc.Devices.Enabled {
		a.tsRelease = release.NewFetcher("tailscale", release.TailscalePkgsURL, ua,
			release.ParseTailscalePkgs, newReleaseHTTPClient(vc.Timeout.D()),
			vc.CacheTTL.D(), releaseLogger, release.WithTracer(a.tracer))
	}
}

// addRuntime builds and appends a per-tailnet runtime (cache, scheduler,
// processors, collectors) and returns it. emitter/card/exportStats come from
// that tailnet's provider; cp carries the capability set + client.
func (a *App) addRuntime(
	name string,
	emitter telemetry.Emitter,
	card *telemetry.CardinalityTracker,
	exportStats func() telemetry.ExportStats,
	cp *provider.Provider,
	multi bool,
) *tailnetRuntime {
	return a.addRuntimeConfigured(
		name,
		name,
		emitter,
		card,
		exportStats,
		func(context.Context) error { return nil },
		cp,
		multi,
	)
}

func (a *App) addRuntimeConfigured(
	configuredName string,
	name string,
	emitter telemetry.Emitter,
	card *telemetry.CardinalityTracker,
	exportStats func() telemetry.ExportStats,
	forceFlush func(context.Context) error,
	cp *provider.Provider,
	multi bool,
) *tailnetRuntime {
	if forceFlush == nil {
		forceFlush = func(context.Context) error { return nil }
	}
	rt := &tailnetRuntime{
		configuredName: configuredName,
		name:           name,
		emitter:        emitter,
		card:           card,
		exportStats:    exportStats,
		forceFlush:     forceFlush,
		cp:             cp,
		apiStats:       NewAPIStats(),
	}
	// Retain the concrete Tailscale client for the Tailscale-only paths
	// (flowFeatureCheck, autoConfigureStreaming). It is nil under provider:
	// headscale, where those paths are gated off by the capability set.
	if tc, ok := cp.Client.(*tsapi.Client); ok {
		rt.client = tc
	}
	webhookDedup := a.webhookDedup
	if a.webhookDedups != nil {
		webhookDedup = dedup.New(auditDedupCapacity)
		a.webhookDedups[configuredName] = webhookDedup
	}
	newRuntime(rt, runtimeDeps{
		cfg:          a.cfg,
		logger:       a.logger,
		tracer:       a.tracer,
		store:        a.store,
		procEmitter:  a.procEmitter,
		rdnsCache:    a.rdnsCache,
		geoDB:        a.geoDB,
		eventStore:   a.eventStore,
		webhookDedup: webhookDedup,
		tsRelease:    a.tsRelease,
		multi:        multi,
		primary:      len(a.runtimes) == 0, // the first runtime owns process-global static targets
	})
	a.runtimes = append(a.runtimes, rt)
	return rt
}

// newApp is the single-runtime assembly seam the unit/integration tests drive
// with an in-memory emitter and a stub provider. The one emitter doubles as both
// the process and tailnet emitter (so a single Recorder observes everything), and
// no telemetry.Provider exists, so the cardinality/export-volume hooks are nil.
func newApp(
	cfg *config.Config,
	version string,
	logger *slog.Logger,
	emitter telemetry.Emitter,
	tracer trace.Tracer,
	shutdown func(context.Context) error,
	cp *provider.Provider,
	store collector.CheckpointStore,
	apiStats *APIStats,
) *App {
	a := newAppShell(cfg, version, logger, emitter, tracer, shutdown, store)
	a.metricGroups = metricGroupMap()
	a.buildProcessDeps()
	rt := a.addRuntimeConfigured(
		cfg.Tailscale.Tailnet,
		"",
		emitter,
		nil,
		nil,
		func(context.Context) error { return nil },
		cp,
		false,
	)
	rt.apiStats = apiStats
	if cfg.SelfObservability.Enabled {
		a.restore = telemetry.InstallExportErrorHandler(emitter, withComponent(a.logger, compTelemetry))
		telemetry.EmitBuildInfo(emitter, version, runtime.Version())
	}
	a.flowDedup = rt.flowDedup
	a.auditDedup = rt.auditDedup
	a.buildReceivers()
	if !cfg.IngressWAL.Enabled {
		a.ingressWAL, _ = newIngressWALCoordinator(nil, nil)
	}
	// Note: a.metricsSrv is intentionally NOT built here — this test seam has no
	// telemetry.ProviderSet, so there is no prometheus gatherer to serve. The real
	// Prometheus endpoint is wired only in New(). See New()'s cfg.Prometheus block.
	if cfg.Admin.Enabled {
		a.adminSrv = a.buildAdminServer()
	}
	return a
}

// Run starts the heartbeat and scheduler, blocks until ctx is canceled, then
// drains and flushes telemetry.
func (a *App) Run(ctx context.Context) error {
	if a.restore != nil {
		defer a.restore()
	}
	// Advisory OAuth-scope preflight (#425). It compares the scopes REQUESTED in
	// config against each enabled collector's documented requirement, so it can
	// only ever warn: the server stays authoritative, and a real gap still shows
	// up at runtime as apistate.StateScopeDenied. It must never block startup —
	// a modeling bug in our scope map would otherwise take down collection.
	LogScopeWarnings(a.logger, a.capabilityMatrix(a.primaryAPIState()))
	// Rotation pollers run for the life of the process. Stopping them here rather
	// than on ctx cancellation lets Stop wait for the goroutine to exit.
	a.credReload.Start()
	defer a.credReload.Stop()
	if a.profiler != nil {
		defer func() { _ = a.profiler.Stop() }()
	}
	if a.geoUpdater != nil {
		// The GeoIP update loop owns no shared state beyond the DB it swaps, so
		// it just needs to stop before the DB is closed. ctx cancellation does
		// that; the goroutine returns on its next select.
		go a.geoUpdater.Run(ctx)
	}
	if a.geoDB != nil {
		defer a.geoDB.Close()
	}
	if a.rdnsCache != nil {
		// Drain background reverse-DNS workers on stop. This deferred Close runs
		// after Run's body returns — i.e. after the schedulers stop AND the receiver
		// goroutines are joined (receiverWG.Wait below) — so no further lookups are
		// issued once it begins (the rdns cache is also shutdown-safe on its own — #121).
		defer a.rdnsCache.Close()
	}
	// Close every tailnet's flow store on the way out. The in-memory ring has
	// nothing to release and returns nil; the persistent backend (#294) uses this
	// to stop its writer and flush what is still queued, so a clean shutdown does
	// not discard connections the emit path already accepted. Deferred here, with
	// the other resource closers, so it runs after the schedulers stop and the
	// receiver goroutines are joined — nothing is still recording by then.
	// Stop the annotation writer on the way out: it flushes the open rollup
	// buckets (so a shutdown does not silently discard events already recorded
	// into a bucket that has not closed yet), drains what is queued under ONE
	// write's budget, and persists the dedupe set. Deferred alongside the other
	// resource closers so nothing is still emitting by the time it runs.
	defer func() {
		if err := a.annotator.Close(context.Background()); err != nil {
			a.logger.Error("close grafana annotation writer", "error", err)
		}
	}()
	defer func() {
		for _, rt := range a.runtimes {
			if rt.flowStore == nil {
				continue
			}
			if err := rt.flowStore.Close(); err != nil {
				a.logger.Error("close flow store", "error", err, "tailnet", a.runtimeName(rt))
			}
		}
	}()
	if a.adminSrv != nil {
		go a.runAdmin(ctx) //nolint:gosec // G118 false positive: runAdmin's only context.Background is the bounded graceful-shutdown context
	}
	if a.metricsSrv != nil {
		go a.runMetrics(ctx) //nolint:gosec // G118 false positive: runMetrics's only context.Background is the bounded graceful-shutdown context
	}

	walEnabled := a.ingressWAL != nil && a.ingressWAL.wal != nil
	var (
		walCancel context.CancelFunc
		walDone   chan error
	)
	if walEnabled {
		startupErr := a.ingressWAL.ReplayStartup(ctx)
		if startupErr != nil {
			if ctx.Err() == nil {
				a.logger.Error("ingress WAL startup replay unavailable", "error", startupErr)
				a.componentError(appcatalog.ComponentIngressWAL)
				<-ctx.Done()
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
			shutdownErr := a.shutdown(shutdownCtx)
			cancel()
			closeErr := a.ingressWAL.Close()
			if errors.Is(startupErr, context.Canceled) ||
				errors.Is(startupErr, context.DeadlineExceeded) {
				startupErr = nil
			}
			return errors.Join(startupErr, shutdownErr, closeErr)
		}

		var walCtx context.Context
		walCtx, walCancel = context.WithCancel(context.Background())
		defer walCancel()
		walDone = make(chan error, 1)
		go func() {
			walDone <- a.ingressWAL.Run(walCtx)
		}()
	}

	interval := a.cfg.OTLP.MetricInterval.D()
	if a.cfg.SelfObservability.Enabled {
		// Process-global self-obs: emitted on the process provider (no tailnet
		// Resource).
		go runHeartbeat(ctx, a.procEmitter, heartbeatInterval, func(e telemetry.Emitter) {
			EmitCapabilityStatus(e, a.capabilityMatrix(a.primaryAPIState()))
		})
		go runRuntimeReporter(ctx, a.procEmitter, interval, readRuntimeStats)
		go runProcessReporter(ctx, a.procEmitter, a.startTime, interval, readProcessCPU)
		go runConfigHealthReporter(ctx, a.cfg, a.procEmitter, interval)
		go runPIIFilterReporter(ctx, a.cfg.PIIFilter, a.procEmitter, interval)
		go runIngressWALReporter(ctx, a.procEmitter, a.ingressWAL, interval)
		// webhook cross-dedup is a process-global, single-tailnet-only set — report it
		// on the process emitter. Each tailnet's own flow/audit dedup sets are
		// reported on THAT runtime's emitter (stamping tailscale.tailnet), so in
		// multi-tailnet mode every tailnet's dedup.size/evictions are visible, not
		// just runtimes[0]'s (#60).
		go runDedupReporter(ctx, a.procEmitter, interval, map[string]*dedup.Set{
			"webhook_cross": a.webhookDedup,
		})
		for _, rt := range a.runtimes {
			go runDedupReporter(ctx, rt.emitter, interval, map[string]*dedup.Set{
				"flow":  rt.flowDedup,
				"audit": rt.auditDedup,
			})
		}
		go runCardinalityReporter(ctx, a.procEmitter, a.procCard, a.metricGroups, interval)
		for _, q := range a.batchQueues {
			go runBatchQueueReporter(ctx, q.emitter, q.tracker, interval)
		}
		go runExportReporter(ctx, a.procEmitter, a.procExportStats, interval)
		// Emit enrich.cache_age at export time (grows while stale) so the staleness
		// alert can fire (#108). Only when the devices collector — the sole cache
		// refresher — is enabled; otherwise the cache never refreshes and the age is
		// not a meaningful signal (matches the old emit-only-when-devices-ran behavior).
		if a.cfg.Collectors.Devices.Enabled {
			go runEnrichCacheAgeReporter(ctx, a.runtimes, interval)
		}
		if a.checkpointEffective == "file" {
			go collector.RunCheckpointReporter(ctx, a.procEmitter, a.cfg.Checkpoint.FilePath, interval)
		}
		// Per-tailnet self-obs: cardinality + export volume ride each tailnet's
		// emitter (stamps tailscale.tailnet on every signal). api.*/scrape.* are already
		// per-tailnet via each client's request hook and the runtime's scheduler.
		for _, rt := range a.runtimes {
			go runCardinalityReporter(ctx, rt.emitter, rt.card, a.metricGroups, interval)
			go runExportReporter(ctx, rt.emitter, rt.exportStats, interval)
		}
	}

	// Short-term runtime/cardinality/throughput/fleet history for the admin status
	// page's sparklines. Introspection-only (no OTLP), so it runs regardless of
	// self-observability — the status page is useful even with self-obs off.
	go runSampler(ctx, a.runtimeHist, samplerInterval, samplerSources{
		read:      readRuntimeStats,
		cardTotal: a.cardinalityTotal,
		perMetric: a.cardinalityPerMetric,
		emit:      a.emitStats,
		fleet:     a.collectorFleet,
	})

	// Version-check loops: gated on their own feature flags (independent of
	// self_observability.enabled — an operator can want update alerts with
	// broad self-obs off).
	if a.selfRelease != nil {
		go a.selfRelease.Run(ctx)
		go runUpdateCheck(ctx, a.procEmitter, a.selfRelease.Latest, a.version, interval)
	}
	if a.tsRelease != nil {
		go a.tsRelease.Run(ctx)
	}

	// Bounded flow-metric rollups (the default output): drain each runtime's
	// accumulator on the export interval. Independent of self-observability — it
	// must run whenever rollup metrics are the configured output.
	if m := a.cfg.Cardinality.Flow.MetricsMode; m == "rollup" || m == "both" {
		for _, rt := range a.runtimes {
			go runRollupFlusher(ctx, rt.flowProc, rt.emitter, interval)
		}
	}

	// receiverWG tracks the stream/webhook receiver goroutines so they are joined
	// AFTER the schedulers stop but BEFORE the telemetry pipeline is shut down and
	// the rdns cache is closed. Their Run(ctx) does a graceful HTTP shutdown that
	// lets in-flight (already-ACKed) requests finish emitting; without joining, a
	// record ACKed to Tailscale but still being processed at shutdown would be
	// dropped when a.shutdown() tears down the exporters first (#53, and #121's
	// "join receivers before closing rdns" criterion).
	var receiverWG sync.WaitGroup
	if a.streamSrv != nil {
		receiverWG.Add(1)
		go func() {
			defer receiverWG.Done()
			a.recordComponentStop(appcatalog.ComponentStream, a.streamSrv.Run(ctx))
		}()
		if a.hasAutoConfigureStreaming() {
			// Off the hot path: registering the sink makes a network call to
			// Tailscale, which must not block the scheduler/other receivers from
			// starting. Bounded so a hung endpoint can't linger past shutdown.
			// Tailscale-only: Headscale has no log-stream API.
			go func() {
				cctx, cancel := context.WithTimeout(ctx, autoConfigureTimeout)
				defer cancel()
				a.autoConfigureStreaming(cctx)
			}()
		}
	}
	if a.webhookSrv != nil {
		receiverWG.Add(1)
		go func() {
			defer receiverWG.Done()
			a.recordComponentStop(appcatalog.ComponentWebhook, a.webhookSrv.Run(ctx))
		}()
	}
	// One scheduler per tailnet, each driving its own registry. Aggregate their
	// exit errors (each returns ctx.Err() on clean stop).
	done := make(chan error, len(a.runtimes))
	for _, rt := range a.runtimes {
		go func(rt *tailnetRuntime) { done <- rt.sched.Run(ctx, rt.registry) }(rt)
	}

	<-ctx.Done()
	var schedErr error
	for range a.runtimes {
		schedErr = errors.Join(schedErr, <-done)
	}
	// The scheduler returns the operator-controlled context's error on stop
	// (SIGINT/SIGTERM cancel it, a deadline expires it); collector failures are
	// isolated and logged, never returned. So any context error here is the
	// normal, clean shutdown signal — not something to report.
	if errors.Is(schedErr, context.Canceled) || errors.Is(schedErr, context.DeadlineExceeded) {
		schedErr = nil
	}

	// Join the receiver goroutines: their graceful HTTP shutdown (triggered by the
	// same ctx cancellation) lets already-ACKed, in-flight requests finish emitting
	// to the processors before we tear anything down. Without this, those records
	// would be lost when a.shutdown() stops the exporters (#53).
	receiverWG.Wait()

	if walEnabled {
		// No receiver can append after the join above. Stop the live worker first
		// so a canceled export attempt releases replay serialization, then perform
		// one bounded final drain over the complete accepted backlog.
		walCancel()
		if err := <-walDone; err != nil {
			a.logger.Warn("ingress WAL worker stopped with a bounded failure")
		}
		drainCtx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		if err := a.ingressWAL.Drain(drainCtx); err != nil {
			a.logger.Warn("ingress WAL final drain incomplete; pending entries remain for restart")
		}
		cancel()
	}

	// Drain each runtime's buffered flow rollup so the final interval's accumulated
	// counts are exported before the telemetry pipeline shuts down. The schedulers
	// AND receivers have stopped (so no connections are still being processed) and
	// this is a no-op in "all" mode (nil accumulator).
	for _, rt := range a.runtimes {
		rt.flowProc.FlushRollup(rt.emitter)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
	shutdownErr := a.shutdown(shutdownCtx)
	cancel()
	var closeErr error
	if a.ingressWAL != nil {
		closeErr = a.ingressWAL.Close()
	}
	return errors.Join(schedErr, shutdownErr, closeErr)
}

// Close flushes and tears down everything New() built, for a caller that
// drives collection directly via RunOnce and never calls Run() — RunOnce's
// only other caller, -preflight/-once (issue #311), needs New()'s telemetry
// pipeline actually flushed (a real -once/-preflight-export export sitting in
// an unflushed OTEL SDK batch would otherwise be lost when the process exits
// right after), and the rdns cache/profiler/ingress WAL New() may have opened
// released, without any of Run()'s schedulers, receivers, or background
// self-obs reporters ever having started. Mirrors the relevant tail of Run's
// own teardown. Safe to call at most once; ctx bounds the flush.
func (a *App) Close(ctx context.Context) error {
	for _, rt := range a.runtimes {
		rt.flowProc.FlushRollup(rt.emitter)
	}
	var closeErr error
	if a.ingressWAL != nil {
		closeErr = a.ingressWAL.Close()
	}
	if a.rdnsCache != nil {
		a.rdnsCache.Close()
	}
	if a.geoDB != nil {
		a.geoDB.Close()
	}
	if a.profiler != nil {
		_ = a.profiler.Stop()
	}
	if a.restore != nil {
		a.restore()
	}
	shutdownErr := a.shutdown(ctx)
	return errors.Join(shutdownErr, closeErr)
}

// autoConfigureStreaming registers this receiver as a Splunk-HEC log-streaming
// sink for both log types via the Tailscale API. It is gated by
// streaming.auto_configure (off by default) and best-effort: a failure is logged
// and does not stop startup. It is only ever called when streaming is enabled and
// public_url is set (enforced by config validation).
func (a *App) autoConfigureStreaming(ctx context.Context) {
	type route struct {
		rt         *tailnetRuntime
		url, token string
	}
	routes := []route{{rt: a.runtimes[0], url: a.cfg.Streaming.PublicURL, token: a.cfg.Streaming.Token.Reveal()}}
	if len(a.cfg.Streaming.Routes) > 0 {
		routes = routes[:0]
		for _, configured := range a.cfg.Streaming.Routes {
			if !configured.AutoConfigure {
				continue
			}
			if rt := a.runtimeFor(configured.Tailnet); rt != nil {
				routes = append(routes, route{rt: rt, url: configured.PublicURL, token: configured.Token.Reveal()})
			}
		}
	}
	for _, route := range routes {
		if route.rt == nil || route.rt.cp.Kind != provider.KindTailscale || route.rt.client == nil {
			continue
		}
		sink := tsapi.LogStreamConfig{DestinationType: "splunk", URL: route.url, Token: route.token}
		for _, logType := range []string{"network", "configuration"} {
			if err := route.rt.client.ConfigureLogStream(ctx, logType, sink); err != nil {
				a.logger.Error("streaming auto_configure failed", "tailnet", a.runtimeName(route.rt), "log_type", logType, "error", err)
				a.componentError(appcatalog.ComponentAutoConfigure)
				continue
			}
			a.logger.Info("streaming auto_configure registered sink", "tailnet", a.runtimeName(route.rt), "log_type", logType)
		}
	}
}

func (a *App) hasAutoConfigureStreaming() bool {
	if len(a.cfg.Streaming.Routes) == 0 {
		return a.cfg.Streaming.AutoConfigure && a.runtimes[0].cp.Kind == provider.KindTailscale && a.runtimes[0].client != nil
	}
	for _, route := range a.cfg.Streaming.Routes {
		if route.AutoConfigure {
			return true
		}
	}
	return false
}

// newReleaseHTTPClient builds the http.Client used by the external release
// fetchers (plain, no Tailscale auth — these are public endpoints).
func newReleaseHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// checkpointStore builds the configured checkpoint store. For store: file it
// ensures the parent directory exists and is writable; if it is not (e.g. a
// read-only root filesystem with no mounted volume, or a local run without
// access to /var/lib), it logs a WARN and falls back to the in-memory store so
// the exporter still runs (window collectors just cold-start from
// initial_lookback after a restart) instead of erroring on every checkpoint write.
// It returns the effective store kind ("file"|"memory") alongside the store, so
// the status page and the checkpoint reporter reflect what is actually in use
// rather than the raw config value (#69). A corrupt/unreadable checkpoint file is
// non-fatal: it is renamed aside and the store starts empty (a cold start),
// instead of crash-looping startup.
func checkpointStore(cfg *config.Config, logger *slog.Logger) (collector.CheckpointStore, checkpointOutcome, error) {
	return checkpointStoreWithDefault(cfg, logger, config.LegacyCheckpointPath)
}

// checkpointOutcome is what the store ACTUALLY resolved to, which can differ
// from the configured values in three ways: a degrade to memory, a relocation
// to the platform state path, or a corrupt file renamed aside. The status page
// and /api/status.json report these rather than the raw config (#69, #336).
type checkpointOutcome struct {
	Kind   string // "file" | "memory"
	Path   string // empty for the memory store
	Reason string // why the effective values differ from the config; empty when they do not
}

// checkpointStoreWithDefault is checkpointStore with the "what counts as the
// default path" seam exposed, so the relocation below can be tested without
// depending on whether the test process can write to /var/lib.
//
// The relocation (#336): the default path is right for the container image,
// which pre-seeds /var/lib/tailscale2otel for uid 65532, and for the Helm
// chart, which sets it explicitly and mounts a volume. It is wrong for a native
// run — Linux non-root cannot create it, and macOS and Windows have no /var/lib
// at all, though releases ship binaries for both. Those runs used to degrade
// silently to in-memory checkpoints and cold-start from initial_lookback on
// every restart.
//
// So when the path is UNTOUCHED BY THE OPERATOR and unwritable, fall back to
// the platform state directory before falling back to memory. Two boundaries
// keep this honest:
//
//   - An explicitly configured path is NEVER relocated. Naming a path is a
//     decision, and it is usually a mounted volume that is briefly absent;
//     writing checkpoints somewhere else would hide that misconfiguration and
//     split state across two locations. Those keep the WARN-and-degrade.
//   - Nothing is ever MOVED. If the default path is usable it is used, so an
//     existing checkpoint can never be stranded by this. The relocation only
//     ever happens where there was no readable checkpoint to begin with.
func checkpointStoreWithDefault(
	cfg *config.Config, logger *slog.Logger, defaultPath string,
) (collector.CheckpointStore, checkpointOutcome, error) {
	if cfg.Checkpoint.Store != "file" || cfg.Checkpoint.FilePath == "" {
		return collector.NewMemoryStore(), checkpointOutcome{Kind: "memory"}, nil
	}
	path, reason := cfg.Checkpoint.FilePath, ""
	if err := ensureWritableDir(filepath.Dir(path)); err != nil {
		relocated := false
		if path == defaultPath {
			if alt := config.DefaultCheckpointPath(); alt != "" && alt != path {
				if altErr := ensureWritableDir(filepath.Dir(alt)); altErr == nil {
					reason = fmt.Sprintf(
						"default checkpoint path %s is not writable; using the platform state path instead", path)
					logger.Info("checkpoint path relocated to the platform state directory "+
						"(the default is container-oriented and a native run usually cannot write it). "+
						"Set checkpoint.file_path explicitly to choose your own.",
						"configured_path", path, "effective_path", alt, "error", err)
					path = alt
					relocated = true
				}
			}
		}
		if !relocated {
			logger.Warn("checkpoint.store=file but the path is not writable; falling back to in-memory checkpoints "+
				"(window cursors will not survive a restart). Mount a writable volume at the directory, or set checkpoint.store=memory to silence this.",
				"file_path", cfg.Checkpoint.FilePath, "error", err)
			return collector.NewMemoryStore(), checkpointOutcome{
				Kind:   "memory",
				Reason: fmt.Sprintf("checkpoint path %s is not writable", cfg.Checkpoint.FilePath),
			}, nil
		}
	}
	store, err := collector.NewFileStore(path)
	if errors.Is(err, collector.ErrCorruptCheckpoint) {
		// Non-critical window-cursor state: rename the corrupt file aside and start
		// from an empty checkpoint (cold start from initial_lookback) rather than
		// fail startup. The dir is writable (checked above), so a fresh file store
		// persists going forward.
		aside := path + ".corrupt"
		if renameErr := os.Rename(path, aside); renameErr != nil {
			logger.Warn("checkpoint file is corrupt and could not be renamed aside; falling back to in-memory checkpoints",
				"file_path", path, "error", err, "rename_error", renameErr)
			return collector.NewMemoryStore(), checkpointOutcome{
				Kind:   "memory",
				Reason: fmt.Sprintf("checkpoint file %s is corrupt and could not be renamed aside", path),
			}, nil
		}
		logger.Warn("checkpoint file was corrupt/unreadable; renamed it aside and started from an empty checkpoint "+
			"(window collectors cold-start from initial_lookback)",
			"file_path", path, "moved_to", aside, "error", err)
		reason = fmt.Sprintf("checkpoint file was corrupt; renamed aside to %s and cold-started", aside)
		store, err = collector.NewFileStore(path)
	}
	if err != nil {
		return nil, checkpointOutcome{}, err
	}
	return store, checkpointOutcome{Kind: "file", Path: path, Reason: reason}, nil
}

// ensureWritableDir creates dir (and parents) if needed and verifies it is
// writable by creating and removing a probe file, so an unwritable path is
// detected once at startup rather than on every checkpoint write.
func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".checkpoint-probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}
