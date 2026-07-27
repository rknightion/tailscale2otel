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
	fmt.Print(renderMarkdownString(changes))
}

// renderMarkdownString builds the Markdown report.
//
// This becomes the body of the scheduled lane's deduplicated tracking issue, read
// by someone months later with no memory of the run that produced it, so it
// carries the evidence to act on without re-running anything (#432): WHERE
// upstream the change is, the old → new values, and what each severity obliges
// the reader to do. Output is deterministic — the lane compares rendered bodies to
// decide whether to update its issue, so churn is indistinguishable from news.
func renderMarkdownString(changes []oas.Change) string {
	if len(changes) == 0 {
		return "No drift detected on consumed operations.\n"
	}
	var b strings.Builder
	b.WriteString("| Severity | Op | Where | Kind | Detail |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, c := range changes {
		where := c.Where
		if where == "" {
			where = "—" // no path to name: neither spec models the operation
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | %s | %s |\n",
			string(c.Severity),
			c.OpID,
			where,
			string(c.Kind),
			c.Detail,
		)
	}

	// Legend, restricted to the severities actually present so a purely
	// informational report does not read as if something were broken.
	b.WriteString("\n**Severities present in this report**\n\n")
	for _, s := range []oas.Severity{oas.Breaking, oas.Warning, oas.Info} {
		if !containsSeverity(changes, s) {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", oas.SeverityAction(s))
	}
	return b.String()
}

// containsSeverity reports whether any change carries the given severity.
func containsSeverity(changes []oas.Change, s oas.Severity) bool {
	for _, c := range changes {
		if c.Severity == s {
			return true
		}
	}
	return false
}

// renderJSON prints the changes as a JSON array to stdout, or a "no drift" message.
func renderJSON(changes []oas.Change) {
	fmt.Println(renderJSONString(changes))
}

// renderJSONString marshals the changes, including Where.
func renderJSONString(changes []oas.Change) string {
	if len(changes) == 0 {
		return "No drift detected on consumed operations."
	}
	out, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "apidrift: marshal json:", err)
		os.Exit(2)
	}
	return string(out)
}
