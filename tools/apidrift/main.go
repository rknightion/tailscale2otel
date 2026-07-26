// Command apidrift reports either response-schema drift on operations
// tailscale2otel consumes or operation-set drift against the committed upstream
// classification baseline.
//
//	apidrift -old vendored.json -new live.json [-format md|json]
//	apidrift -operations -new live.json [-format md|json]
//
// Exit codes: 0 = no actionable drift (clean or info-only); 3 = Breaking/Warning
// changes present; 2 = usage/IO error.
//
// Response-schema classification always uses contract.ConsumedOpIDs(), so the
// tool and runtime cannot diverge on what "consumed" means. Operation discovery
// separately inventories every verb in the candidate spec.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rknightion/tailscale2otel/v3/internal/oas"
	"github.com/rknightion/tailscale2otel/v3/internal/tsapi/contract"
)

func main() {
	oldPath := flag.String("old", "", "path to baseline OpenAPI JSON")
	newPath := flag.String("new", "", "path to candidate OpenAPI JSON")
	format := flag.String("format", "md", "output format: md|json")
	operations := flag.Bool("operations", false, "classify the candidate's operation set against the committed baseline")
	flag.Parse()

	if *newPath == "" || (!*operations && *oldPath == "") {
		fmt.Fprintln(os.Stderr, "-new is always required; -old is also required unless -operations is set")
		os.Exit(2)
	}

	newSpec, err := loadSpec(*newPath)
	if err != nil {
		check(fmt.Errorf("loading -new: %w", err))
	}

	if *operations {
		base, err := contract.EmbeddedOperationDispositions()
		if err != nil {
			check(fmt.Errorf("loading operation baseline: %w", err))
		}
		problems := contract.ValidateOperationDispositions(
			base,
			contract.OperationInventory(newSpec),
			contract.ConsumedOpIDs(),
		)
		renderOperationProblems(*format, problems)
		if len(problems) > 0 {
			os.Exit(3)
		}
		return
	}

	oldSpec, err := loadSpec(*oldPath)
	if err != nil {
		check(fmt.Errorf("loading -old: %w", err))
	}
	changes := oas.Classify(oldSpec, newSpec, contract.ConsumedOpIDs())
	render(*format, changes)

	if oas.HasActionable(changes) {
		os.Exit(3)
	}
}

// renderOperationProblems prints operation-set drift separately from response
// schema drift so scheduled CI can maintain a distinct deduplicated issue.
func renderOperationProblems(format string, problems []string) {
	if len(problems) == 0 {
		fmt.Println("No drift detected in the upstream operation set.")
		return
	}
	if format == "json" {
		out, err := json.MarshalIndent(problems, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "apidrift: marshal operation problems:", err)
			os.Exit(2)
		}
		fmt.Println(string(out))
		return
	}
	fmt.Println("## Upstream operation-set drift")
	fmt.Println()
	for _, problem := range problems {
		fmt.Printf("- %s\n", problem)
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
