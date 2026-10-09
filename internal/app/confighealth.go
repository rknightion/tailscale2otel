package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// emitConfigHealth records the config.warnings and config.valid gauges for the
// current state of cfg. It is a pure function of cfg and e — safe to call from
// tests directly.
func emitConfigHealth(e telemetry.Emitter, cfg *config.Config) {
	warnings := float64(len(cfg.Warnings()))
	e.Gauge(
		appcatalog.DocConfigWarnings.Name,
		appcatalog.DocConfigWarnings.Unit,
		appcatalog.DocConfigWarnings.Description,
		warnings,
		nil,
	)

	var validVal float64
	if cfg.Validate() == nil {
		validVal = 1
	}
	e.Gauge(
		appcatalog.DocConfigValid.Name,
		appcatalog.DocConfigValid.Unit,
		appcatalog.DocConfigValid.Description,
		validVal,
		nil,
	)
}

// runConfigHealthReporter emits config-health self-metrics immediately and then
// on each interval until ctx is canceled. If interval <= 0 it defaults to 60s.
// Mirrors runHeartbeat exactly for the goroutine/ticker shape.
func runConfigHealthReporter(ctx context.Context, cfg *config.Config, e telemetry.Emitter, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	emit := func() {
		emitConfigHealth(e, cfg)
	}
	emit()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emit()
		}
	}
}

// emitConfigAdvisories emits one WARN log event per active configuration
// advisory. Startup already writes the same advisories through slog, but that
// happens before the OTLP pipeline exists, so they never reach the log backend;
// the caller invokes this once the process emitter is up.
func emitConfigAdvisories(e telemetry.Emitter, cfg *config.Config) {
	for _, adv := range cfg.Advisories() {
		e.LogEvent(telemetry.Event{
			Name:     appcatalog.EventConfigAdvisory,
			Severity: telemetry.SeverityWarn,
			Body:     adv.Message,
			Attrs:    telemetry.Attrs{appcatalog.AttrConfigKey: adv.Key},
		})
	}
}

// EmitConfigAdvisories re-emits the startup advisories through the process
// emitter. Call it once after New, so the OTLP log pipeline is already running.
func (a *App) EmitConfigAdvisories() {
	if a.procEmitter == nil {
		return
	}
	emitConfigAdvisories(a.procEmitter, a.cfg)
}
