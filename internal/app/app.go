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
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/stream"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
	"github.com/rknightion/tailscale2otel/internal/webhook"
)

const heartbeatInterval = 15 * time.Second

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
	a.flowProc = flowlog.NewProcessor(a.cache, flowOptions(cfg))
	a.auditProc = audit.NewProcessor()
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

func checkpointStore(cfg *config.Config) (collector.CheckpointStore, error) {
	if cfg.Checkpoint.Store == "file" && cfg.Checkpoint.FilePath != "" {
		return collector.NewFileStore(cfg.Checkpoint.FilePath)
	}
	return collector.NewMemoryStore(), nil
}
