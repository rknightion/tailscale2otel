// Command promqlcheck parses every query expression in the repo's generated
// observability artifacts with the real Prometheus parser, so a malformed
// expression fails CI instead of shipping as a panel that silently shows an
// error (or worse, "No data") in someone's Grafana.
//
// It walks:
//
//	deploy/grafana/tailscale2otel-tailnet.json  Grafana dashboard schema v2
//	deploy/grafana/tailscale2otel-health.json   Grafana dashboard schema v2
//	deploy/alerts/grafana-managed/*.json  Grafana-managed rule manifests
//	                                      (rules.alerting.grafana.app/v0alpha1)
//
// and for each expression it (1) substitutes Grafana templating tokens for
// stand-ins that parse in the position they appear (see substitute.go), (2)
// rejects any reference to a template variable the dashboard does not declare,
// and (3) parses the result with github.com/prometheus/prometheus.
//
// # Why LogQL and TraceQL are NOT parsed
//
// There is no usable Go LogQL parser to depend on. github.com/grafana/loki/v3
// exports one, but importing it pulls 580+ modules (Kubernetes, AWS, etcd,
// Consul, gRPC gateways) into a tool whose job is to parse strings, and it does
// not even compile as a library: loki's own go.mod carries `replace` directives
// for hashicorp/memberlist and friends which do not apply to consumers, so
// github.com/grafana/dskit fails to build against the memberlist version a
// downstream module resolves. Verified against loki v3 on 2026-07-27. TraceQL
// has the same problem via github.com/grafana/tempo.
//
// So LogQL and TraceQL expressions are extracted, counted, and variable-checked
// (the undeclared-variable gate is language-agnostic), but their SYNTAX is not
// validated. The summary states this in its own block on every run that sees
// one — a silent skip would let a green run read as full coverage.
//
// # Usage
//
// promqlcheck is a separate Go module, so it cannot be run as
// `go run ./tools/promqlcheck` from the repo root. From the repo root:
//
//	go run -C tools/promqlcheck . -root "$PWD"       # check; non-zero on failure
//	go run -C tools/promqlcheck . -root "$PWD" -v    # also list every expression
//
// Exit codes: 0 = every expression checked passes; 1 = at least one failed;
// 2 = usage or IO error (a missing artifact, unparseable JSON/YAML).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
)

// The artifacts to walk, repo-relative. Adding one here is the whole change
// needed to bring another generated file under the gate.
const (
	// The rules are Grafana-managed manifests, one JSON file per rule, plus a
	// folder manifest that carries no expressions and is skipped.
	rulesDir       = "deploy/alerts/grafana-managed"
	folderManifest = "_folder.json"
)

// dashboardPaths is the dashboard family (#526). Every expression on every
// shipped dashboard is parsed with the real prometheus/promql parser; missing
// one artifact would leave half of them unchecked while the gate still reported
// success.
var dashboardPaths = []string{
	"deploy/grafana/tailscale2otel-tailnet.json",
	"deploy/grafana/tailscale2otel-health.json",
}

func main() {
	root := flag.String("root", ".", "repository root to resolve the artifact paths against")
	verbose := flag.Bool("v", false, "list every expression checked, with its source and language")
	flag.Parse()

	failures, err := run(os.Stdout, *root, *verbose)
	if err != nil {
		fmt.Fprintln(os.Stderr, "promqlcheck:", err)
		os.Exit(2)
	}
	if failures > 0 {
		os.Exit(1)
	}
}

// ruleManifests lists the shipped rule manifests, repo-relative, excluding the
// folder manifest. It errors on an empty result rather than returning one: with
// no manifests the tool would print "0 failures" having parsed nothing, which is
// indistinguishable from a clean run.
func ruleManifests(root string) ([]string, error) {
	dir := filepath.Join(root, filepath.FromSlash(rulesDir))
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", dir, err)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if filepath.Base(m) == folderManifest {
			continue
		}
		out = append(out, path.Join(rulesDir, filepath.Base(m)))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no rule manifests; nothing to check", rulesDir)
	}
	sort.Strings(out)
	return out, nil
}

// run checks every artifact under root, writes the report to w, and returns the
// failure count. A returned error means an artifact could not be read or
// structurally understood; per-expression problems come back as failures.
func run(w io.Writer, root string, verbose bool) (int, error) {
	rep := newReport()

	checks := make([]struct {
		path string
		fn   func(*Report, string, []byte) error
	}, 0, len(dashboardPaths))
	for _, p := range dashboardPaths {
		checks = append(checks, struct {
			path string
			fn   func(*Report, string, []byte) error
		}{p, checkDashboard})
	}

	// One manifest per rule, so the rule leg is a directory walk rather than a
	// fixed path. An empty directory is an ERROR, not a clean run: the tool would
	// otherwise report "0 failures" having checked no rule at all, which reads as
	// a pass.
	manifests, err := ruleManifests(root)
	if err != nil {
		return 0, err
	}
	for _, m := range manifests {
		checks = append(checks, struct {
			path string
			fn   func(*Report, string, []byte) error
		}{m, checkGrafanaRules})
	}

	for _, c := range checks {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.path)))
		if err != nil {
			return 0, fmt.Errorf("read artifact: %w", err)
		}
		if err := c.fn(rep, c.path, data); err != nil {
			return 0, err
		}
	}

	rep.write(w, verbose)
	return len(rep.Failures), nil
}
