// Command tailscale2otel polls the Tailscale API and exports OTEL metrics + logs.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/rknightion/tailscale2otel/internal/app"
	"github.com/rknightion/tailscale2otel/internal/config"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "path", *configPath, "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := app.New(ctx, cfg, version, logger)
	if err != nil {
		logger.Error("failed to initialize", "error", err)
		os.Exit(1)
	}

	logger.Info("tailscale2otel starting",
		"version", version, "tailnet", cfg.Tailscale.Tailnet, "otlp_protocol", cfg.OTLP.Protocol)
	if err := application.Run(ctx); err != nil {
		logger.Error("exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("tailscale2otel stopped")
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
