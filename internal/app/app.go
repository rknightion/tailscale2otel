// Package app wires configuration, telemetry, the Tailscale client, the device
// cache, and the collector scheduler into a runnable service.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/grafana/pyroscope-go"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/rdns"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/stream"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
	"github.com/rknightion/tailscale2otel/internal/webhook"
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
	cfg         *config.Config
	version     string    // injected build version, for the status page
	startTime   time.Time // process start, for uptime on the status page
	emitter     telemetry.Emitter
	card        *telemetry.CardinalityTracker // active-series tracker; nil when self-obs disabled
	shutdown    func(context.Context) error   // flushes telemetry on stop
	restore     func()                        // restores the prior otel error handler
	client      *tsapi.Client
	cache       *enrich.DeviceCache
	registry    *collector.Registry
	sched       *collector.Scheduler
	status      *collector.StatusTracker  // per-collector run outcomes, for the status page
	runtimeHist *runtimeHistory           // short-term runtime/cardinality trends, for the status page
	apiStats    *APIStats                 // per-endpoint Tailscale API request stats, for the status page
	store       collector.CheckpointStore // checkpoint store; read for window-collector state on the status page
	logger      *slog.Logger

	flowProc     *flowlog.Processor
	auditProc    *audit.Processor
	flowDedup    *dedup.Set             // flow cross-source set; surfaced on the status page
	auditDedup   *dedup.Set             // audit cross-source set; surfaced on the status page
	nodeMetrics  *nodemetrics.Collector // nil unless the node-metrics collector is enabled
	streamSrv    *stream.Server
	webhookSrv   *webhook.Server
	webhookDedup *dedup.Set // shared cross-source set (webhook<->audit); nil unless enabled
	adminSrv     *http.Server
	profiler     *pyroscope.Profiler // pyroscope push profiler; nil unless enabled
	rdnsCache    *rdns.Cache         // async reverse-DNS cache; nil unless enrichment.reverse_dns.enabled
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
)

// withComponent returns a logger that tags every record with its subsystem name.
func withComponent(l *slog.Logger, component string) *slog.Logger {
	return l.With(semconv.AttrComponent, component)
}

func New(ctx context.Context, cfg *config.Config, version string, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	topts := telemetryOptions(cfg, version)
	topts.Logger = withComponent(logger, compTelemetry) // surfaces Emitter label-collision diagnostics
	provider, err := telemetry.NewProvider(ctx, topts)
	if err != nil {
		return nil, err
	}
	emitter := provider.Emitter()

	// One combined request hook: APIStats always records (the status page's API
	// panel must work even with self-obs off), and apiObserver additionally emits
	// OTLP only when self-observability is enabled.
	apiStats := NewAPIStats()
	tsOpts := tsapiOptions(cfg)
	tsOpts.Logger = withComponent(logger, compTSAPI)
	var obs func(string, int, int, time.Duration)
	if cfg.SelfObservability.Enabled {
		obs = apiObserver(emitter)
	}
	tsOpts.OnRequest = func(i tsapi.RequestInfo) {
		if obs != nil {
			obs(i.Endpoint, i.Status, i.Attempts, i.Duration)
		}
		apiStats.Record(i)
	}
	client, err := tsapi.NewClient(tsOpts)
	if err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}
	store, err := checkpointStore(cfg, withComponent(logger, compCheckpoint))
	if err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}

	a := newApp(cfg, version, logger, emitter, provider.Shutdown, client, store, apiStats)
	a.card = provider.Cardinality()

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

// newApp assembles an App from already-constructed dependencies. It is the seam
// the integration test drives with an in-memory emitter and a stub Tailscale
// client.
func newApp(
	cfg *config.Config,
	version string,
	logger *slog.Logger,
	emitter telemetry.Emitter,
	shutdown func(context.Context) error,
	client *tsapi.Client,
	store collector.CheckpointStore,
	apiStats *APIStats,
) *App {
	if logger == nil {
		logger = slog.Default()
	}
	a := &App{
		cfg:         cfg,
		version:     version,
		startTime:   time.Now(),
		emitter:     emitter,
		shutdown:    shutdown,
		client:      client,
		cache:       enrich.NewDeviceCache(),
		registry:    collector.NewRegistry(),
		status:      collector.NewStatusTracker(),
		runtimeHist: newRuntimeHistory(runtimeHistoryLen),
		apiStats:    apiStats,
		store:       store,
		logger:      logger,
	}
	a.sched = collector.NewScheduler(emitter, store,
		collector.WithLogger(withComponent(logger, compCollector)),
		collector.WithSelfObs(cfg.SelfObservability.Enabled),
		collector.WithStatusTracker(a.status))
	if cfg.SelfObservability.Enabled {
		a.restore = telemetry.InstallExportErrorHandler(emitter, withComponent(logger, compTelemetry))
		telemetry.EmitBuildInfo(emitter, runtime.Version())
	}
	// Shared cross-source de-duplication: the same flow window / audit event can
	// arrive from both the poll collector and the streaming receiver, which share
	// one processor each, so a dedup set on the processor suppresses the repeat.
	// The sets are retained on App so the status page can report their occupancy.
	a.flowDedup = dedup.New(flowDedupCapacity)
	fopts := flowOptions(cfg)
	fopts.Dedup = a.flowDedup
	// Opt-in reverse-DNS enrichment of external flow addresses. The async cache is
	// retained on App so Run can drain its background workers on shutdown.
	if cfg.Enrichment.ReverseDNS.Enabled {
		ropts := rdnsOptions(cfg)
		// Emit the cache's self-obs metrics only when self-observability is on; the
		// admin status page reads Stats() directly regardless.
		if cfg.SelfObservability.Enabled {
			ropts.Emitter = emitter
		}
		a.rdnsCache = rdns.New(ropts)
		fopts.RDNS = a.rdnsCache
	}
	a.flowProc = flowlog.NewProcessor(a.cache, fopts)
	a.auditDedup = dedup.New(auditDedupCapacity)
	auditOpts := []audit.Option{audit.WithDedup(a.auditDedup)}
	if cfg.Webhook.Enabled && cfg.Webhook.DedupAuditEvents {
		// Best-effort cross-SOURCE de-dup so a change reported by BOTH a webhook
		// and the audit logs is counted once. Off by default; the same set is
		// handed to the webhook server in buildReceivers.
		a.webhookDedup = dedup.New(auditDedupCapacity)
		auditOpts = append(auditOpts, audit.WithCrossDedup(a.webhookDedup))
	}
	a.auditProc = audit.NewProcessor(auditOpts...)
	a.registerCollectors()
	a.buildReceivers()
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
		// Drain background reverse-DNS workers on stop (after the scheduler and
		// receivers have wound down, so no further lookups are issued).
		defer a.rdnsCache.Close()
	}
	if a.cfg.SelfObservability.Enabled {
		go runHeartbeat(ctx, a.emitter, heartbeatInterval)
		go runCardinalityReporter(ctx, a.emitter, a.card, a.cfg.OTLP.MetricInterval.D())
		go runRuntimeReporter(ctx, a.emitter, a.cfg.OTLP.MetricInterval.D(), readRuntimeStats)
		go runDedupReporter(ctx, a.emitter, a.cfg.OTLP.MetricInterval.D(), map[string]*dedup.Set{
			"flow":          a.flowDedup,
			"audit":         a.auditDedup,
			"webhook_cross": a.webhookDedup,
		})
	}

	// Short-term runtime/cardinality history for the admin status page's
	// sparklines. Introspection-only (no OTLP), so it runs regardless of
	// self-observability — the status page is useful even with self-obs off.
	go runSampler(ctx, a.runtimeHist, samplerInterval, readRuntimeStats, a.cardinalityTotal)

	// Bounded flow-metric rollups (the default output): drain the accumulator on
	// the export interval. Independent of self-observability — it must run whenever
	// rollup metrics are the configured output.
	if m := a.cfg.Cardinality.Flow.MetricsMode; m == "rollup" || m == "both" {
		go runRollupFlusher(ctx, a.flowProc, a.emitter, a.cfg.OTLP.MetricInterval.D())
	}

	if a.streamSrv != nil {
		go func() {
			a.recordReceiverStop(appcatalog.ComponentStream, a.streamSrv.Run(ctx))
		}()
		if a.cfg.Streaming.AutoConfigure {
			// Off the hot path: registering the sink makes a network call to
			// Tailscale, which must not block the scheduler/other receivers from
			// starting. Bounded so a hung endpoint can't linger past shutdown.
			go func() {
				cctx, cancel := context.WithTimeout(ctx, autoConfigureTimeout)
				defer cancel()
				a.autoConfigureStreaming(cctx)
			}()
		}
	}
	if a.webhookSrv != nil {
		go func() {
			a.recordReceiverStop(appcatalog.ComponentWebhook, a.webhookSrv.Run(ctx))
		}()
	}
	if a.adminSrv != nil {
		go a.runAdmin(ctx) //nolint:gosec // G118 false positive: runAdmin's only context.Background is the bounded graceful-shutdown context
	}

	done := make(chan error, 1)
	go func() { done <- a.sched.Run(ctx, a.registry) }()

	<-ctx.Done()
	schedErr := <-done
	// The scheduler returns the operator-controlled context's error on stop
	// (SIGINT/SIGTERM cancel it, a deadline expires it); collector failures are
	// isolated and logged, never returned. So any context error here is the
	// normal, clean shutdown signal — not something to report.
	if errors.Is(schedErr, context.Canceled) || errors.Is(schedErr, context.DeadlineExceeded) {
		schedErr = nil
	}

	// Drain any buffered flow rollup so the final interval's accumulated counts are
	// exported before the telemetry pipeline shuts down. The scheduler has stopped
	// (so no connections are still being processed) and this is a no-op in "all"
	// mode (nil accumulator).
	a.flowProc.FlushRollup(a.emitter)

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
		if err := a.client.ConfigureLogStream(ctx, logType, sink); err != nil {
			a.logger.Error("streaming auto_configure failed", "log_type", logType, "error", err)
			a.componentError(appcatalog.ComponentAutoConfigure)
			continue
		}
		a.logger.Info("streaming auto_configure registered sink", "log_type", logType, "url", sink.URL)
	}
}

// checkpointStore builds the configured checkpoint store. For store: file it
// ensures the parent directory exists and is writable; if it is not (e.g. a
// read-only root filesystem with no mounted volume, or a local run without
// access to /var/lib), it logs a WARN and falls back to the in-memory store so
// the exporter still runs (window collectors just cold-start from
// initial_lookback after a restart) instead of erroring on every checkpoint write.
func checkpointStore(cfg *config.Config, logger *slog.Logger) (collector.CheckpointStore, error) {
	if cfg.Checkpoint.Store != "file" || cfg.Checkpoint.FilePath == "" {
		return collector.NewMemoryStore(), nil
	}
	if err := ensureWritableDir(filepath.Dir(cfg.Checkpoint.FilePath)); err != nil {
		logger.Warn("checkpoint.store=file but the path is not writable; falling back to in-memory checkpoints "+
			"(window cursors will not survive a restart). Mount a writable volume at the directory, or set checkpoint.store=memory to silence this.",
			"file_path", cfg.Checkpoint.FilePath, "error", err)
		return collector.NewMemoryStore(), nil
	}
	return collector.NewFileStore(cfg.Checkpoint.FilePath)
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
