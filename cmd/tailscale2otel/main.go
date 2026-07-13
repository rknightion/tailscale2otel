// Command tailscale2otel polls the Tailscale API and exports OTEL metrics + logs.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/rknightion/tailscale2otel/v2/internal/app"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses flags and dispatches to one of three modes: -version (print and
// exit), -validate (load+validate the config and exit without starting the
// server), or the normal server run. Splitting it out of main lets the flag
// modes be exercised by tests without touching os.Args or process exit.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tailscale2otel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to the YAML config file (empty = env-only defaults)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	validateOnly := fs.Bool("validate", false, "load and validate the config, print errors/warnings, and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	if *validateOnly {
		return runValidate(*configPath, stdout, stderr)
	}

	return runServer(*configPath, stderr)
}

// runValidate loads the config at path via the same internal/config.Load path
// the server uses, prints any Warnings() to stdout, and reports the load/
// validate error (if any) to stderr. It never starts the exporter.
func runValidate(configPath string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "config invalid: %v\n", err)
		return 1
	}

	for _, w := range cfg.Warnings() {
		fmt.Fprintf(stdout, "WARN: %s\n", w)
	}
	fmt.Fprintln(stdout, "config OK")
	return 0
}

// runServer runs the exporter until it exits or its context is canceled.
func runServer(configPath string, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "path", configPath, "error", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)

	for _, w := range cfg.Warnings() {
		logger.Warn("configuration advisory", "warning", w)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := app.New(ctx, cfg, version, logger)
	if err != nil {
		logger.Error("failed to initialize", "error", err)
		return 1
	}

	logger.Info("tailscale2otel starting",
		"version", version, "tailnet", cfg.Tailscale.Tailnet, "otlp_protocol", cfg.OTLP.Protocol)
	if err := application.Run(ctx); err != nil {
		logger.Error("exited with error", "error", err)
		return 1
	}
	logger.Info("tailscale2otel stopped")
	return 0
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
