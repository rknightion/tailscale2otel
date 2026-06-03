// Package app wires configuration, telemetry, the Tailscale client, the device
// cache, and the collector scheduler into a runnable service.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
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

// App is the assembled, runnable service.
type App struct {
	cfg      *config.Config
	emitter  telemetry.Emitter
	shutdown func(context.Context) error // flushes telemetry on stop
	restore  func()                      // restores the prior otel error handler
	client   *tsapi.Client
	cache    *enrich.DeviceCache
	registry *collector.Registry
	sched    *collector.Scheduler
	logger   *slog.Logger

	flowProc   *flowlog.Processor
	auditProc  *audit.Processor
	streamSrv  *stream.Server
	webhookSrv *webhook.Server
	adminSrv   *http.Server
}

// New assembles the service from cfg. The caller owns ctx for the lifetime of
// construction; Run takes its own ctx.
func New(ctx context.Context, cfg *config.Config, version string, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	provider, err := telemetry.NewProvider(ctx, telemetryOptions(cfg, version))
	if err != nil {
		return nil, err
	}
	emitter := provider.Emitter()

	tsOpts := tsapiOptions(cfg)
	if cfg.SelfObservability.Enabled {
		tsOpts.OnRequest = apiObserver(emitter)
	}
	client, err := tsapi.NewClient(tsOpts)
	if err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}
	store, err := checkpointStore(cfg)
	if err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}

	return newApp(cfg, version, logger, emitter, provider.Shutdown, client, store), nil
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
) *App {
	if logger == nil {
		logger = slog.Default()
	}
	a := &App{
		cfg:      cfg,
		emitter:  emitter,
		shutdown: shutdown,
		client:   client,
		cache:    enrich.NewDeviceCache(),
		registry: collector.NewRegistry(),
		sched: collector.NewScheduler(emitter, store,
			collector.WithLogger(logger),
			collector.WithSelfObs(cfg.SelfObservability.Enabled)),
		logger: logger,
	}
	if cfg.SelfObservability.Enabled {
		a.restore = telemetry.InstallExportErrorHandler(emitter)
		telemetry.EmitBuildInfo(emitter, version, runtime.Version())
	}
	// Shared cross-source de-duplication: the same flow window / audit event can
	// arrive from both the poll collector and the streaming receiver, which share
	// one processor each, so a dedup set on the processor suppresses the repeat.
	fopts := flowOptions(cfg)
	fopts.Dedup = dedup.New(flowDedupCapacity)
	a.flowProc = flowlog.NewProcessor(a.cache, fopts)
	a.auditProc = audit.NewProcessor(audit.WithDedup(dedup.New(auditDedupCapacity)))
	a.registerCollectors()
	a.buildReceivers()
	if cfg.Admin.Enabled {
		a.adminSrv = newAdminServer(cfg.Admin.Listen)
	}
	return a
}

// Run starts the heartbeat and scheduler, blocks until ctx is cancelled, then
// drains and flushes telemetry.
func (a *App) Run(ctx context.Context) error {
	if a.restore != nil {
		defer a.restore()
	}
	if a.cfg.SelfObservability.Enabled {
		go runHeartbeat(ctx, a.emitter, heartbeatInterval)
	}

	if a.streamSrv != nil {
		go func() {
			if err := a.streamSrv.Run(ctx); err != nil {
				a.logger.Error("stream receiver stopped", "error", err)
			}
		}()
		if a.cfg.Streaming.AutoConfigure {
			a.autoConfigureStreaming(ctx)
		}
	}
	if a.webhookSrv != nil {
		go func() {
			if err := a.webhookSrv.Run(ctx); err != nil {
				a.logger.Error("webhook receiver stopped", "error", err)
			}
		}()
	}
	if a.adminSrv != nil {
		go a.runAdmin(ctx)
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
		Token:           a.cfg.Streaming.Token,
	}
	for _, logType := range []string{"network", "configuration"} {
		if err := a.client.ConfigureLogStream(ctx, logType, sink); err != nil {
			a.logger.Error("streaming auto_configure failed", "log_type", logType, "error", err)
			continue
		}
		a.logger.Info("streaming auto_configure registered sink", "log_type", logType, "url", sink.URL)
	}
}

func checkpointStore(cfg *config.Config) (collector.CheckpointStore, error) {
	if cfg.Checkpoint.Store == "file" && cfg.Checkpoint.FilePath != "" {
		return collector.NewFileStore(cfg.Checkpoint.FilePath)
	}
	return collector.NewMemoryStore(), nil
}
