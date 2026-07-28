// Command configcheck validates one or more tailscale2otel YAML config files by
// running the real internal/config.Load on each. It exits non-zero (printing the
// first error per file) if any file fails to load, parse, or validate.
//
// This is a CI-only tool kept in a separate Go module so it never affects the
// main module's `go build ./...`. It exercises the cross-field validation rules
// that JSON Schema draft-07 (used by values.schema.json) cannot express, and is
// run against both config.example.yaml and the Helm-rendered ConfigMap config.
//
// Usage:
//
//	configcheck path/to/config.yaml [more.yaml ...]
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

func main() {
	os.Exit(check(os.Args[1:], os.Stdout, os.Stderr))
}

// check validates every path and returns the process exit code: 0 when all load,
// 1 when any fails, 2 for a usage error.
//
// The body lives here rather than in main so it can be tested. Without this the
// module's `go test` leg would pass vacuously — a gate that cannot fail (#437).
// Writers are injected for the same reason.
//
// Per file, every config.Diagnostic is reported (#307) instead of just the
// first error — config.Load's own Validate() call still returns (nil, err) on
// a validation failure (Load is out of this change's scope; see the
// "config.go wiring" note in the #307 delivery report), so a file that fails
// to decode/pre-validate at all degrades to the historical single-error FAIL
// line, same as before. A file that decodes but fails Validate() rules gets
// every diagnostic, one line each, followed by a FAIL summary line.
func check(paths []string, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "usage: configcheck <config.yaml> [config2.yaml ...]")
		return 2
	}

	failed := false
	for _, p := range paths {
		cfg, err := config.Load(p)
		if cfg == nil {
			fmt.Fprintf(stderr, "FAIL %s: %v\n", p, err)
			failed = true
			continue
		}

		diags := cfg.Diagnostics()
		for _, d := range diags {
			sev := strings.ToUpper(string(d.Severity))
			w := stdout
			if d.Severity == config.SeverityError {
				w = stderr
			}
			line := sev
			if d.Path != "" {
				line += " " + d.Path
			}
			line += ": " + d.Message
			if d.Remediation != "" {
				line += " — " + d.Remediation
			}
			fmt.Fprintf(w, "%s %s\n", p, line)
		}
		if config.HasErrors(diags) {
			// A summary FAIL line per file, mirroring the OK line below. The
			// per-diagnostic lines above say WHAT is wrong; this says which
			// file is rejected, which is what a caller checking many files
			// greps for — and what the pre-diagnostics output always emitted.
			fmt.Fprintf(stderr, "FAIL %s\n", p)
			failed = true
			continue
		}
		fmt.Fprintf(stdout, "OK   %s\n", p)
	}

	if failed {
		return 1
	}
	return 0
}
