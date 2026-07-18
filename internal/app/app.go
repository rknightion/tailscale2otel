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

	"github.com/rknightion/tailscale2otel/v2/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/hsapi"
	"github.com/rknightion/tailscale2otel/v2/internal/provider"
	"github.com/rknightion/tailscale2otel/v2/internal/rdns"
	"github.com/rknightion/tailscale2otel/v2/internal/release"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/stream"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
	"github.com/rknightion/tailscale2otel/v2/internal/webhook"
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
	procEmitter     telemetry.Emitter
	procCard        *telemetry.CardinalityTracker // process provider's tracker; nil when self-obs off
	procExportStats func() telemetry.ExportStats  // process provider's export volume; nil when self-obs off
	metricGroups    map[string]string             // metric source-name -> catalog group, for series.by_group rollup

	shutdown    func(context.Context) error // flushes telemetry on stop
	restore     func()                      // restores the prior otel error handler
	runtimeHist *runtimeHistory             // short-term runtime/cardinality trends, for the status page
	store       collector.CheckpointStore   // checkpoint store; read for window-collector state on the status page
	// checkpointEffective is the store kind actually in use ("file"|"memory"),
	// which can differ from cfg.Checkpoint.Store after a fallback (unwritable path
	// or a corrupt file). The status page and the checkpoint reporter use this, not
	// the raw config value (#69).
	checkpointEffective string
	logger              *slog.Logger

	flowDedup    *dedup.Set // runtimes[0] flow set, retained for the dedup self-obs reporter
	auditDedup   *dedup.Set // runtimes[0] audit set, retained for the dedup self-obs reporter
	streamSrv    *stream.Server
	webhookSrv   *webhook.Server
	webhookDedup *dedup.Set // shared cross-source set (webhook<->audit); nil unless enabled
	adminSrv     *http.Server
	metricsSrv   *http.Server        // prometheus pull endpoint; nil unless prometheus.enabled
	profiler     *pyroscope.Profiler // pyroscope push profiler; nil unless enabled
	rdnsCache    *rdns.Cache         // async reverse-DNS cache; nil unless enrichment.reverse_dns.enabled

	selfRelease *release.Fetcher // nil unless version_checks.self.enabled
	tsRelease   *release.Fetcher // nil unless version_checks.devices.enabled

	// readyState tracks terminal stream/webhook receiver failures so /readyz
	// reports unready after a receiver fails to bind or stops unexpectedly (#57).
	// Written by recordReceiverStop, read by the readyz handler.
	readyState *receiverHealth
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
)

// withComponent returns a logger that tags every record with its subsystem name.
func withComponent(l *slog.Logger, component string) *slog.Logger {
	return l.With(semconv.AttrComponent, component)
}

func New(ctx context.Context, cfg *config.Config, version string, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
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
	base.Logger = withComponent(logger, compTelemetry) // surfaces Emitter label-collision diagnostics
	perTN := make([]telemetry.PerTailnetOptions, len(resolved))
	for i := range resolved {
		perTN[i] = telemetry.PerTailnetOptions{
			Name:       labels[i],
			InstanceID: instanceFor(base.InstanceID, labels[i], multi),
		}
	}
	ps, err := telemetry.NewProviderSet(ctx, base, perTN)
	if err != nil {
		return nil, err
	}
	store, checkpointEffective, err := checkpointStore(cfg, withComponent(logger, compCheckpoint))
	if err != nil {
		_ = ps.Shutdown(ctx)
		return nil, err
	}

	a := newAppShell(cfg, version, logger, ps.Process().Emitter(), ps.Process().Tracer(), ps.Shutdown, store)
	a.checkpointEffective = checkpointEffective
	a.procCard = ps.Process().Cardinality()
	a.procExportStats = ps.Process().ExportStats
	a.metricGroups = metricGroupMap()
	a.buildProcessDeps()

	// Build one runtime per tailnet (Tailscale), or a single Headscale runtime.
	if cfg.Provider == "headscale" {
		hsClient := hsapi.NewClient(hsapiOptions(cfg))
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
		a.addRuntime("", a.procEmitter, nil, nil, cp, multi)
	} else {
		for i, rt := range resolved {
			label := labels[i]
			tp := ps.Tailnet(label)
			emitter := tp.Emitter()
			apiStats := NewAPIStats()
			cp, err := buildTailscaleProvider(rt, version, logger, a.tracer, emitter, apiStats, cfg.SelfObservability.Enabled)
			if err != nil {
				_ = ps.Shutdown(ctx)
				// Attribute the failure to the offending tailnet so an MSP with many
				// entries knows which one to fix (e.g. a mis-mounted secret) instead of
				// a bare "no authentication configured" — #125.
				return nil, fmt.Errorf("tailnets[%d] %q: %w", i, label, err)
			}
			r := a.addRuntime(label, emitter, tp.Cardinality(), tp.ExportStats, cp, multi)
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
	a.buildReceivers()
	if cfg.Admin.Enabled {
		a.adminSrv = a.buildAdminServer()
	}
	if cfg.Prometheus.Enabled {
		if g := ps.PromGatherers(); len(g) > 0 {
			a.metricsSrv = a.buildMetricsServer(g)
		}
	}

	// Continuous profiling is opt-in. startProfiling also applies the runtime
	// mutex/block sampling rates needed by the /debug/pprof pull path. A failure
	// to reach Pyroscope is non-fatal: the exporter's core job is unaffected.
	profLogger := withComponent(logger, compProfiling)
	prof, err := startProfiling(cfg, version, profLogger)
	if err != nil {
		profLogger.Error("pyroscope profiler failed to start", "error", err)
	}
	a.profiler = prof
	return a, nil
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
		readyState:  newReceiverHealth(),
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
	if cfg.Webhook.Enabled && cfg.Webhook.DedupAuditEvents {
		// Best-effort cross-SOURCE de-dup so a change reported by BOTH a webhook and
		// the audit logs is counted once (single-tailnet only; webhook requires it).
		a.webhookDedup = dedup.New(auditDedupCapacity)
	}
	vc := cfg.VersionChecks
	ua := "tailscale2otel/" + a.version
	releaseLogger := withComponent(a.logger, compRelease)
	if vc.Self.Enabled {
		a.selfRelease = release.NewFetcher("self", release.GitHubLatestURL, ua,
			release.ParseGitHubLatest, newReleaseHTTPClient(vc.Timeout.D()),
			vc.CacheTTL.D(), releaseLogger)
	}
	if vc.Devices.Enabled {
		a.tsRelease = release.NewFetcher("tailscale", release.TailscalePkgsURL, ua,
			release.ParseTailscalePkgs, newReleaseHTTPClient(vc.Timeout.D()),
			vc.CacheTTL.D(), releaseLogger)
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
	rt := &tailnetRuntime{
		name:        name,
		emitter:     emitter,
		card:        card,
		exportStats: exportStats,
		cp:          cp,
		apiStats:    NewAPIStats(),
	}
	// Retain the concrete Tailscale client for the Tailscale-only paths
	// (flowFeatureCheck, autoConfigureStreaming). It is nil under provider:
	// headscale, where those paths are gated off by the capability set.
	if tc, ok := cp.Client.(*tsapi.Client); ok {
		rt.client = tc
	}
	newRuntime(rt, runtimeDeps{
		cfg:          a.cfg,
		logger:       a.logger,
		tracer:       a.tracer,
		store:        a.store,
		procEmitter:  a.procEmitter,
		rdnsCache:    a.rdnsCache,
		webhookDedup: a.webhookDedup,
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
	rt := a.addRuntime("", emitter, nil, nil, cp, false)
	rt.apiStats = apiStats
	if cfg.SelfObservability.Enabled {
		a.restore = telemetry.InstallExportErrorHandler(emitter, withComponent(a.logger, compTelemetry))
		telemetry.EmitBuildInfo(emitter, version, runtime.Version())
	}
	a.flowDedup = rt.flowDedup
	a.auditDedup = rt.auditDedup
	a.buildReceivers()
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
	if a.profiler != nil {
		defer func() { _ = a.profiler.Stop() }()
	}
	if a.rdnsCache != nil {
		// Drain background reverse-DNS workers on stop. This deferred Close runs
		// after Run's body returns — i.e. after the schedulers stop AND the receiver
		// goroutines are joined (receiverWG.Wait below) — so no further lookups are
		// issued once it begins (the rdns cache is also shutdown-safe on its own — #121).
		defer a.rdnsCache.Close()
	}
	interval := a.cfg.OTLP.MetricInterval.D()
	if a.cfg.SelfObservability.Enabled {
		// Process-global self-obs: emitted on the process provider (no tailnet
		// Resource).
		go runHeartbeat(ctx, a.procEmitter, heartbeatInterval)
		go runRuntimeReporter(ctx, a.procEmitter, interval, readRuntimeStats)
		go runProcessReporter(ctx, a.procEmitter, a.startTime, interval, readProcessCPU)
		go runConfigHealthReporter(ctx, a.cfg, a.procEmitter, interval)
		go runPIIFilterReporter(ctx, a.cfg.PIIFilter, a.procEmitter, interval)
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

	// Short-term runtime/cardinality history for the admin status page's
	// sparklines. Introspection-only (no OTLP), so it runs regardless of
	// self-observability — the status page is useful even with self-obs off.
	go runSampler(ctx, a.runtimeHist, samplerInterval, readRuntimeStats, a.cardinalityTotal, a.cardinalityPerMetric)

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
			a.recordReceiverStop(appcatalog.ComponentStream, a.streamSrv.Run(ctx))
		}()
		if a.cfg.Streaming.AutoConfigure && a.runtimes[0].cp.Kind == provider.KindTailscale && a.runtimes[0].client != nil {
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
			a.recordReceiverStop(appcatalog.ComponentWebhook, a.webhookSrv.Run(ctx))
		}()
	}
	if a.adminSrv != nil {
		go a.runAdmin(ctx) //nolint:gosec // G118 false positive: runAdmin's only context.Background is the bounded graceful-shutdown context
	}
	if a.metricsSrv != nil {
		go a.runMetrics(ctx) //nolint:gosec // G118 false positive: runMetrics's only context.Background is the bounded graceful-shutdown context
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

	// Drain each runtime's buffered flow rollup so the final interval's accumulated
	// counts are exported before the telemetry pipeline shuts down. The schedulers
	// AND receivers have stopped (so no connections are still being processed) and
	// this is a no-op in "all" mode (nil accumulator).
	for _, rt := range a.runtimes {
		rt.flowProc.FlushRollup(rt.emitter)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(schedErr, a.shutdown(shutdownCtx))
}

// autoConfigureStreaming registers this receiver as a Splunk-HEC log-streaming
// sink for both log types via the Tailscale API. It is gated by
// streaming.auto_configure (off by default) and best-effort: a failure is logged
// and does not stop startup. It is only ever called when streaming is enabled and
// public_url is set (enforced by config validation).
func (a *App) autoConfigureStreaming(ctx context.Context) {
	sink := tsapi.LogStreamConfig{
		DestinationType: "splunk",
		URL:             a.cfg.Streaming.PublicURL,
		Token:           a.cfg.Streaming.Token.Reveal(),
	}
	for _, logType := range []string{"network", "configuration"} {
		if err := a.runtimes[0].client.ConfigureLogStream(ctx, logType, sink); err != nil {
			a.logger.Error("streaming auto_configure failed", "log_type", logType, "error", err)
			a.componentError(appcatalog.ComponentAutoConfigure)
			continue
		}
		a.logger.Info("streaming auto_configure registered sink", "log_type", logType, "url", sink.URL)
	}
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
func checkpointStore(cfg *config.Config, logger *slog.Logger) (collector.CheckpointStore, string, error) {
	if cfg.Checkpoint.Store != "file" || cfg.Checkpoint.FilePath == "" {
		return collector.NewMemoryStore(), "memory", nil
	}
	if err := ensureWritableDir(filepath.Dir(cfg.Checkpoint.FilePath)); err != nil {
		logger.Warn("checkpoint.store=file but the path is not writable; falling back to in-memory checkpoints "+
			"(window cursors will not survive a restart). Mount a writable volume at the directory, or set checkpoint.store=memory to silence this.",
			"file_path", cfg.Checkpoint.FilePath, "error", err)
		return collector.NewMemoryStore(), "memory", nil
	}
	store, err := collector.NewFileStore(cfg.Checkpoint.FilePath)
	if errors.Is(err, collector.ErrCorruptCheckpoint) {
		// Non-critical window-cursor state: rename the corrupt file aside and start
		// from an empty checkpoint (cold start from initial_lookback) rather than
		// fail startup. The dir is writable (checked above), so a fresh file store
		// persists going forward.
		aside := cfg.Checkpoint.FilePath + ".corrupt"
		if renameErr := os.Rename(cfg.Checkpoint.FilePath, aside); renameErr != nil {
			logger.Warn("checkpoint file is corrupt and could not be renamed aside; falling back to in-memory checkpoints",
				"file_path", cfg.Checkpoint.FilePath, "error", err, "rename_error", renameErr)
			return collector.NewMemoryStore(), "memory", nil
		}
		logger.Warn("checkpoint file was corrupt/unreadable; renamed it aside and started from an empty checkpoint "+
			"(window collectors cold-start from initial_lookback)",
			"file_path", cfg.Checkpoint.FilePath, "moved_to", aside, "error", err)
		store, err = collector.NewFileStore(cfg.Checkpoint.FilePath)
	}
	if err != nil {
		return nil, "", err
	}
	return store, "file", nil
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
