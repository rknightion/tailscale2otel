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
	"os"

	"github.com/rknightion/tailscale2otel/internal/config"
)

func main() {
	paths := os.Args[1:]
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "usage: configcheck <config.yaml> [config2.yaml ...]")
		os.Exit(2)
	}

	failed := false
	for _, p := range paths {
		if _, err := config.Load(p); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", p, err)
			failed = true
			continue
		}
		fmt.Printf("OK   %s\n", p)
	}

	if failed {
		os.Exit(1)
	}
}
