package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/app"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// Exit codes for -preflight/-once (issue #311). Distinct on purpose: the
// operator's next action differs by class — 3 says "fix the credential", 4
// says "the credential works, a specific collector doesn't", 5 says "the
// collectors are fine, the export path is broken". When more than one class
// fails at once, the LOWEST-numbered non-zero code wins (it is the most
// upstream cause) — see exitCodeFor.
const (
	pfOK              = 0
	pfConfigInvalid   = 1
	pfUsage           = 2
	pfAuthFailure     = 3
	pfCollectFailure  = 4
	pfExportFailure   = 5
	pfCloseBudget     = 10 * time.Second
	defaultPfDeadline = 60 * time.Second
)

// preflightReport is the -json output shape for -preflight/-once. Kept flat
// and stable: one object, a fixed set of top-level fields, and a flat array
// of per-(tailnet, collector) results.
//
// Schema:
//
//	{
//	  "mode": "preflight" | "once",
//	  "ok": bool,
//	  "exit_code": int,
//	  "deadline": "60s",                 // the -preflight-timeout value, Go duration string
//	  "export_attempted": bool,          // false for -preflight without -preflight-export
//	  "acl_validate_skipped": bool,      // -preflight forced collectors.acl.validate off
//	  "close_error": string,             // non-empty only on a failed telemetry flush/teardown
//	  "results": [
//	    {
//	      "tailnet": string,
//	      "collector": string,
//	      "ok": bool,
//	      "duration_ms": int64,
//	      "auth_failure": bool,          // true only for an unambiguous 401
//	      "error": string                // empty when ok
//	    }
//	  ]
//	}
type preflightReport struct {
	Mode            string `json:"mode"`
	OK              bool   `json:"ok"`
	ExitCode        int    `json:"exit_code"`
	Deadline        string `json:"deadline"`
	ExportAttempted bool   `json:"export_attempted"`
	// ACLValidateSkipped discloses that -preflight forced collectors.acl.validate
	// off, so this run proves nothing about whether the ACL policy validates.
	ACLValidateSkipped bool                 `json:"acl_validate_skipped,omitempty"`
	CloseError         string               `json:"close_error,omitempty"`
	Results            []preflightResultRow `json:"results"`
}

type preflightResultRow struct {
	Tailnet     string `json:"tailnet"`
	Collector   string `json:"collector"`
	OK          bool   `json:"ok"`
	DurationMS  int64  `json:"duration_ms"`
	AuthFailure bool   `json:"auth_failure"`
	Error       string `json:"error,omitempty"`
}

// runPreflight implements -preflight and -once: load+validate the config,
// build the tailnet runtimes with every inbound listener and (for -preflight)
// checkpoint persistence and profiling forced off (see app.PrepareConfig), run
// exactly one collection cycle of every enabled collector (app.RunOnce), then
// report and return the exit code. once selects -once's semantics (real
// export path + real checkpoint behavior); otherwise this is -preflight,
// which additionally forces an in-memory checkpoint and, unless
// preflightExport is set, discards telemetry instead of exporting it for
// real. It never starts an admin/prometheus/streaming/webhook listener and
// never mutates the control plane — every registered collector is read-only
// by construction (see internal/collector/CLAUDE.md).
func runPreflight(
	configPath string,
	once, preflightExport, jsonOut bool,
	timeout time.Duration,
	version string,
	stdout, stderr io.Writer,
) int {
	mode := "preflight"
	if once {
		mode = "once"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return reportEarlyFailure(mode, jsonOut, timeout, pfConfigInvalid,
			fmt.Sprintf("config invalid: %v", err), stdout, stderr)
	}

	logger := slog.New(newLogHandler(stderr, cfg.LogFormat, parseLevel(cfg.LogLevel)))

	// -preflight's extra suppressions (in-memory checkpoint, no Pyroscope, no
	// ACL validate POST) apply always, independent of -preflight-export:
	// exporting and side-effect-freeness are separate axes. -once keeps all
	// three exactly as configured.
	runCfg := app.PrepareConfig(cfg, !once)
	// Disclosed, not silent: with ACL validation suppressed, a green preflight
	// says nothing about whether the policy validates, and an operator who
	// assumed otherwise would find out in production.
	aclValidateSkipped := !once && app.ACLValidateSuppressed(cfg)

	// exportReal is true for -once (always) and for -preflight when
	// -preflight-export was passed; otherwise telemetry is redirected to a
	// discarding stdout exporter so New()'s normal wiring never dials the
	// configured OTLP backend.
	exportReal := once || preflightExport
	var opts []app.Option
	if !exportReal {
		opts = append(opts, app.WithTelemetryOverride(func(o telemetry.Options) telemetry.Options {
			o.Protocol = "stdout"
			o.StdoutWriter = io.Discard
			return o
		}))
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	application, err := app.New(ctx, runCfg, version, logger, opts...)
	if err != nil {
		// New() fails only on a construction-time problem building the
		// control-plane client/provider (a malformed credential shape, a
		// missing workload-identity token file, ...) — closer to "the
		// credential/endpoint is wrong" than to a specific collector failing,
		// so it is classified as an auth/control-plane failure.
		return reportEarlyFailure(mode, jsonOut, timeout, pfAuthFailure,
			fmt.Sprintf("failed to initialize: %v", err), stdout, stderr)
	}

	results := application.RunOnce(ctx)

	closeCtx, closeCancel := context.WithTimeout(context.Background(), pfCloseBudget)
	closeErr := application.Close(closeCtx)
	closeCancel()

	exitCode, ok := exitCodeFor(results, exportReal, closeErr)
	printPreflightReport(mode, ok, exitCode, timeout, exportReal, aclValidateSkipped, closeErr, results, jsonOut, stdout)
	return exitCode
}

// exitCodeFor derives the exit code from RunOnce's results plus (when export
// was attempted) any Close error, applying the "lowest-numbered non-zero code
// wins" rule from the CLAUDE.md/issue spec: 3 (auth) is more upstream than 4
// (collection), which is more upstream than 5 (export).
func exitCodeFor(results []app.CollectorRunResult, exportReal bool, closeErr error) (code int, ok bool) {
	var candidates []int
	anyAuth, anyCollectFail := false, false
	for _, r := range results {
		if r.OK {
			continue
		}
		if r.AuthFailure {
			anyAuth = true
		} else {
			anyCollectFail = true
		}
	}
	if anyAuth {
		candidates = append(candidates, pfAuthFailure)
	}
	if anyCollectFail {
		candidates = append(candidates, pfCollectFailure)
	}
	if exportReal && closeErr != nil {
		candidates = append(candidates, pfExportFailure)
	}
	if len(candidates) == 0 {
		return pfOK, true
	}
	return slices.Min(candidates), false
}

// reportEarlyFailure prints a minimal report for a failure that happens
// before any collector could run (bad config, or App construction itself
// failed) and returns the given exit code.
func reportEarlyFailure(mode string, jsonOut bool, timeout time.Duration, code int, msg string, stdout, stderr io.Writer) int {
	if jsonOut {
		rep := preflightReport{
			Mode:     mode,
			OK:       false,
			ExitCode: code,
			Deadline: timeout.String(),
			Results:  []preflightResultRow{},
		}
		rep.CloseError = msg
		enc := json.NewEncoder(stdout)
		_ = enc.Encode(rep)
		return code
	}
	fmt.Fprintf(stderr, "%s: %s\n", mode, msg)
	return code
}

// printPreflightReport writes the human or JSON report for a completed run
// (every collector was at least attempted or explicitly recorded as skipped
// past the deadline).
func printPreflightReport(
	mode string,
	ok bool,
	exitCode int,
	timeout time.Duration,
	exportReal bool,
	aclValidateSkipped bool,
	closeErr error,
	results []app.CollectorRunResult,
	jsonOut bool,
	stdout io.Writer,
) {
	if jsonOut {
		rows := make([]preflightResultRow, 0, len(results))
		for _, r := range results {
			row := preflightResultRow{
				Tailnet:     r.Tailnet,
				Collector:   r.Collector,
				OK:          r.OK,
				DurationMS:  r.Duration.Milliseconds(),
				AuthFailure: r.AuthFailure,
			}
			if r.Err != nil {
				row.Error = r.Err.Error()
			}
			rows = append(rows, row)
		}
		rep := preflightReport{
			Mode:            mode,
			OK:              ok,
			ExitCode:        exitCode,
			Deadline:        timeout.String(),
			ExportAttempted: exportReal,
			Results:         rows,
		}
		rep.ACLValidateSkipped = aclValidateSkipped
		if closeErr != nil {
			rep.CloseError = closeErr.Error()
		}
		enc := json.NewEncoder(stdout)
		_ = enc.Encode(rep)
		return
	}

	for _, r := range results {
		marker := "OK  "
		if !r.OK {
			marker = "FAIL"
		}
		if r.Err != nil {
			fmt.Fprintf(stdout, "[%s] %-12s %-20s %8s  %v\n", marker, r.Tailnet, r.Collector, r.Duration.Round(time.Millisecond), r.Err)
		} else {
			fmt.Fprintf(stdout, "[%s] %-12s %-20s %8s\n", marker, r.Tailnet, r.Collector, r.Duration.Round(time.Millisecond))
		}
	}
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
	}
	fmt.Fprintf(stdout, "%s: %s (%d/%d collectors ok, export_attempted=%t, exit=%d)\n",
		mode, verdict, countOK(results), len(results), exportReal, exitCode)
	if aclValidateSkipped {
		fmt.Fprintf(stdout, "%s: collectors.acl.validate was skipped — it is the one non-GET "+
			"control-plane request, so preflight does not make it. This run does NOT prove the "+
			"ACL policy validates; use -once for that.\n", mode)
	}
	if closeErr != nil {
		fmt.Fprintf(stdout, "%s: telemetry flush/teardown error: %v\n", mode, closeErr)
	}
}

func countOK(results []app.CollectorRunResult) int {
	n := 0
	for _, r := range results {
		if r.OK {
			n++
		}
	}
	return n
}
