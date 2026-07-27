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
func check(paths []string, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "usage: configcheck <config.yaml> [config2.yaml ...]")
		return 2
	}

	failed := false
	for _, p := range paths {
		if _, err := config.Load(p); err != nil {
			fmt.Fprintf(stderr, "FAIL %s: %v\n", p, err)
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
