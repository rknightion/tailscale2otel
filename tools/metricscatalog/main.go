// Command metricscatalog keeps docs/metrics.md derived from the code rather than
// hand-maintained: it renders the in-code telemetry catalog (internal/catalog —
// every emitting package's metricdoc descriptors) into the generated metric/log
// tables delimited by the "<!-- BEGIN GENERATED: ... -->" / "<!-- END GENERATED -->"
// markers, preserving all prose outside the markers.
//
// This is a CI-only tool kept in a separate Go module so it never affects the
// main module's `go build ./...`.
//
// Usage (run from the repo root):
//
//	metricscatalog            # print the regenerated doc to stdout (dry run)
//	metricscatalog -write     # rewrite docs/metrics.md in place (like helm-docs)
//	metricscatalog -check     # exit non-zero if docs/metrics.md is out of date
//	metricscatalog -file path # operate on a different file
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rknightion/tailscale2otel/internal/catalog"
)

func main() {
	check := flag.Bool("check", false, "verify the doc is in sync with the code catalog; exit non-zero on drift")
	write := flag.Bool("write", false, "rewrite the doc in place from the code catalog")
	file := flag.String("file", "docs/metrics.md", "path to the metrics/logs reference doc")
	flag.Parse()

	if err := run(*file, *check, *write); err != nil {
		fmt.Fprintln(os.Stderr, "metricscatalog:", err)
		os.Exit(1)
	}
}

func run(path string, check, write bool) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := catalog.Render(string(src))
	if err != nil {
		return err
	}

	switch {
	case write:
		if out == string(src) {
			fmt.Printf("%s already up to date\n", path)
			return nil
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil { //nolint:gosec // G306: generated docs file is intentionally world-readable
			return err
		}
		fmt.Printf("wrote %s\n", path)
		return nil
	case check:
		if out != string(src) {
			return fmt.Errorf("%s is out of date with the in-code telemetry catalog; regenerate it with `go run ./tools/metricscatalog -write` (run from the repo root) and commit the result", path)
		}
		fmt.Printf("%s is in sync\n", path)
		return nil
	default:
		fmt.Print(out)
		return nil
	}
}
