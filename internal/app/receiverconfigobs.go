package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/listenaddr"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

func receiverCredentialMissing(cfg *config.Config, receiver string) (enabled, missing bool) {
	switch receiver {
	case "streaming":
		if !cfg.Streaming.Enabled {
			return false, false
		}
		missing = cfg.Streaming.Token == ""
		if len(cfg.Streaming.Routes) > 0 {
			missing = false
			for _, route := range cfg.Streaming.Routes {
				missing = missing || route.Token == ""
			}
		}
		return true, missing && !listenaddr.IsLoopback(cfg.Streaming.Listen)
	case "webhook":
		if !cfg.Webhook.Enabled {
			return false, false
		}
		missing = cfg.Webhook.Secret == ""
		if len(cfg.Webhook.Routes) > 0 {
			missing = false
			for _, route := range cfg.Webhook.Routes {
				missing = missing || route.Secret == ""
			}
		}
		return true, missing && !listenaddr.IsLoopback(cfg.Webhook.Listen)
	default:
		return false, false
	}
}

func emitReceiverConfig(e telemetry.Emitter, cfg *config.Config) {
	for _, receiver := range []string{"streaming", "webhook"} {
		enabled, missing := receiverCredentialMissing(cfg, receiver)
		if !enabled {
			continue
		}
		value := 0.0
		if missing {
			value = 1
		}
		e.Gauge(appcatalog.DocReceiverMisconfigured.Name, appcatalog.DocReceiverMisconfigured.Unit,
			appcatalog.DocReceiverMisconfigured.Description, value, telemetry.Attrs{"receiver": receiver})
	}
}

func runReceiverConfigReporter(ctx context.Context, cfg *config.Config, e telemetry.Emitter, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	emitReceiverConfig(e, cfg)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emitReceiverConfig(e, cfg)
		}
	}
}
