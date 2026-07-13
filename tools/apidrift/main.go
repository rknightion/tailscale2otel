// Command apidrift diffs a Tailscale OpenAPI spec against a newer one, restricted
// to the operations tailscale2otel consumes, and reports drift.
//
//	apidrift -old vendored.json -new live.json [-format md|json]
//
// Exit codes: 0 = no actionable drift (clean or info-only); 3 = Breaking/Warning
// changes present; 2 = usage/IO error. The -ops default is the consumed-surface
// manifest, so the tool and runtime never diverge.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rknightion/tailscale2otel/v2/internal/oas"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi/contract"
)

func main() {
	oldPath := flag.String("old", "", "path to baseline OpenAPI JSON")
	newPath := flag.String("new", "", "path to candidate OpenAPI JSON")
	format := flag.String("format", "md", "output format: md|json")
	flag.Parse()

	if *oldPath == "" || *newPath == "" {
		fmt.Fprintln(os.Stderr, "both -old and -new are required")
		os.Exit(2)
	}

	oldSpec, err := loadSpec(*oldPath)
	if err != nil {
		check(fmt.Errorf("loading -old: %w", err))
	}
	newSpec, err := loadSpec(*newPath)
	if err != nil {
		check(fmt.Errorf("loading -new: %w", err))
	}

	changes := oas.Classify(oldSpec, newSpec, contract.ConsumedOpIDs())
	render(*format, changes)

	if oas.HasActionable(changes) {
		os.Exit(3)
	}
}

// loadSpec reads a file and parses it as an OpenAPI JSON document.
func loadSpec(path string) (*oas.Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	spec, err := oas.ParseSpec(b)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}
	return spec, nil
}

// check prints err to stderr and exits with code 2 (IO/usage error).
func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "apidrift:", err)
		os.Exit(2)
	}
}

// render writes the changes to stdout in the requested format.
func render(format string, changes []oas.Change) {
	switch format {
	case "json":
		renderJSON(changes)
	default:
		renderMarkdown(changes)
	}
}

// renderMarkdown prints a Markdown table to stdout, or a "no drift" message.
func renderMarkdown(changes []oas.Change) {
	if len(changes) == 0 {
		fmt.Println("No drift detected on consumed operations.")
		return
	}
	var b strings.Builder
	b.WriteString("| Severity | Op | Kind | Detail |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, c := range changes {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			string(c.Severity),
			c.OpID,
			string(c.Kind),
			c.Detail,
		)
	}
	fmt.Print(b.String())
}

// renderJSON prints the changes as a JSON array to stdout, or a "no drift" message.
func renderJSON(changes []oas.Change) {
	if len(changes) == 0 {
		fmt.Println("No drift detected on consumed operations.")
		return
	}
	out, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "apidrift: marshal json:", err)
		os.Exit(2)
	}
	fmt.Println(string(out))
}
