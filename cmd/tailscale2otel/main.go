// Command tailscale2otel polls the Tailscale API and exports OTEL metrics + logs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/app"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses flags and dispatches to one of nine modes: -version (print and
// exit), -healthcheck (probe the local admin readiness endpoint and exit),
// -validate (load+validate the config and exit without starting the server),
// -print-effective-config (render the redacted effective config, optionally
// with per-key provenance, and exit; see effectiveconfig.go), -preflight
// (build the tailnet runtimes and run one collection cycle of every enabled
// collector, side-effect-free by default; see preflight.go), -once (the same
// single cycle, but with the real export/checkpoint behavior),
// -prometheus-check (run one side-effect-free cycle, then validate the local
// Prometheus exposition without binding its listener),
// -adopt-flow-db (claim one tailnet's pre-hardening flow database and exit;
// see adoptflowdb.go), or the normal server run. -preflight and -once are
// mutually exclusive. Splitting it out
// of main lets the flag modes be exercised by tests without touching
// os.Args or process exit.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tailscale2otel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to the YAML config file (empty = env-only defaults)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	validateOnly := fs.Bool("validate", false, "load and validate the config, print errors/warnings, and exit")
	healthcheck := fs.Bool("healthcheck", false, "probe the local admin readiness endpoint and exit (0 ready, 1 unready, 2 unreachable)")
	healthcheckTimeout := fs.Duration("healthcheck-timeout", 5*time.Second, "overall deadline for -healthcheck")
	preflight := fs.Bool("preflight", false, "load+validate the config, build the tailnet runtimes, authenticate, and run one collection cycle of every enabled collector, then exit; no listener is started, nothing is exported unless -preflight-export, and no checkpoint is persisted")
	once := fs.Bool("once", false, "run one real collection cycle (configured export path and checkpoint behavior) and exit; no listener is started")
	preflightExport := fs.Bool("preflight-export", false, "with -preflight, actually export through the configured OTLP path instead of discarding it (ignored without -preflight)")
	preflightTimeout := fs.Duration("preflight-timeout", defaultPfDeadline, "overall deadline for -preflight, -once, or -prometheus-check")
	prometheusCheck := fs.Bool("prometheus-check", false, "run one side-effect-free collection cycle and validate the local Prometheus exposition without starting its listener")
	jsonOut := fs.Bool("json", false, "with -preflight, -once, or -prometheus-check, emit one machine-readable JSON report instead of the human-readable one; with -validate, emit the config.Diagnostics() array instead of human-readable lines (#307)")
	warningsAsErrors := fs.Bool("warnings-as-errors", false, "with -validate, exit 1 if any advisory warning is present, not just hard errors (#307)")
	printEffectiveConfig := fs.Bool("print-effective-config", false, "load the config, render every effective key through the same redaction rules the admin status page uses, print it, and exit; never exposes secret values (#309)")
	effectiveConfigFormat := fs.String("print-effective-config-format", "json", "output format for -print-effective-config: \"json\" or \"yaml\"")
	effectiveConfigProvenance := fs.Bool("print-effective-config-provenance", false, "with -print-effective-config, also report which layer (default, file, or env) produced each key's effective value — never a secret's content (#309)")
	adoptFlowDB := fs.String("adopt-flow-db", "", "claim the named tailnet's pre-hardening flow database (flows-<slug>.db), stamp its tailnet identity, move it to the name the store reads, and exit; naming the tailnet is the ownership assertion the legacy filename cannot make")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	if *healthcheck {
		return runHealthcheck(*configPath, *healthcheckTimeout, stdout, stderr)
	}

	if *validateOnly {
		return runValidate(*configPath, *jsonOut, *warningsAsErrors, stdout, stderr)
	}

	if *printEffectiveConfig {
		return runPrintEffectiveConfig(*configPath, *effectiveConfigFormat, *effectiveConfigProvenance, stdout, stderr)
	}

	// Presence, not emptiness. `-adopt-flow-db=` with no value must be an error,
	// not a silent fall-through to the normal server run: an operator who meant
	// to adopt a database and fumbled the argument would otherwise get a
	// started exporter and no adoption, with nothing saying so.
	if flagWasSet(fs, "adopt-flow-db") {
		if *adoptFlowDB == "" {
			fmt.Fprintln(stderr, "-adopt-flow-db needs the tailnet that owns the database")
			return 2
		}
		return runAdoptFlowDB(*configPath, *adoptFlowDB, stdout, stderr)
	}

	if *preflight && *once {
		fmt.Fprintln(stderr, "-preflight and -once are mutually exclusive")
		return 2
	}
	if *prometheusCheck && (*preflight || *once) {
		fmt.Fprintln(stderr, "-prometheus-check is mutually exclusive with -preflight and -once")
		return 2
	}
	if *prometheusCheck {
		return runPrometheusCheck(*configPath, *jsonOut, *preflightTimeout, version, stdout, stderr)
	}
	if *preflight || *once {
		return runPreflight(*configPath, *once, *preflightExport, *jsonOut, *preflightTimeout, version, stdout, stderr)
	}

	return runServer(*configPath, stderr)
}

// flagWasSet reports whether name was given on the command line at all, which
// is the only way to tell an omitted string flag from one explicitly set to
// the empty string.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// runValidate loads the config at path via the same internal/config.Load path
// the server uses and reports every configuration Diagnostic in one pass
// (#307) instead of stopping at the first error. It never starts the
// exporter.
//
// config.Load returns the decoded Config alongside a Validate() error, so a
// file that parses but breaks a rule still reports every OTHER problem in the
// same pass. A nil config means the failure was earlier than that — the file
// could not be read or decoded — and there is nothing to run diagnostics on,
// so that case degrades to the single load error.
func runValidate(configPath string, jsonOut, warningsAsErrors bool, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if cfg == nil {
		fmt.Fprintf(stderr, "config invalid: %v\n", err)
		return 1
	}

	diags := cfg.Diagnostics()
	hasErrors := config.HasErrors(diags)
	hasWarnings := config.HasWarnings(diags)

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(diags); encErr != nil {
			fmt.Fprintf(stderr, "encode diagnostics: %v\n", encErr)
			return 1
		}
	} else {
		for _, d := range diags {
			line := strings.ToUpper(string(d.Severity))
			if d.Path != "" {
				line += " " + d.Path
			}
			line += ": " + d.Message
			if d.Remediation != "" {
				line += " — " + d.Remediation
			}
			// Errors to stderr, advisories to stdout — the same split
			// tools/configcheck uses, and the stream -validate has always
			// written its failure to. Putting errors on stdout would make
			// `tailscale2otel -validate >/dev/null` silently discard the very
			// thing it was run to find.
			w := stdout
			if d.Severity == config.SeverityError {
				w = stderr
			}
			fmt.Fprintln(w, line)
		}
		if !hasErrors {
			fmt.Fprintln(stdout, "config OK")
		}
	}

	if hasErrors || (warningsAsErrors && hasWarnings) {
		return 1
	}
	return 0
}

// runServer runs the exporter until it exits or its context is canceled.
func runServer(configPath string, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "path", configPath, "error", err)
		return 1
	}

	logger := slog.New(newLogHandler(stderr, cfg.LogFormat, parseLevel(cfg.LogLevel)))
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
