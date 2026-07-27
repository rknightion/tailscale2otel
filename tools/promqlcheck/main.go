// Command promqlcheck parses every query expression in the repo's generated
// observability artifacts with the real Prometheus parser, so a malformed
// expression fails CI instead of shipping as a panel that silently shows an
// error (or worse, "No data") in someone's Grafana.
//
// It walks three files:
//
//	deploy/grafana/tailscale2otel.json               Grafana dashboard schema v2
//	deploy/alerts/tailscale2otel.grafana-rules.yaml  Grafana provisioning rules
//	deploy/alerts/tailscale2otel.rules.yaml          plain Prometheus rules
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
	"path/filepath"
)

// The artifacts to walk, repo-relative. Adding one here is the whole change
// needed to bring another generated file under the gate.
const (
	dashboardPath    = "deploy/grafana/tailscale2otel.json"
	grafanaRulesPath = "deploy/alerts/tailscale2otel.grafana-rules.yaml"
	promRulesPath    = "deploy/alerts/tailscale2otel.rules.yaml"
)

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

// run checks every artifact under root, writes the report to w, and returns the
// failure count. A returned error means an artifact could not be read or
// structurally understood; per-expression problems come back as failures.
func run(w io.Writer, root string, verbose bool) (int, error) {
	rep := newReport()

	checks := []struct {
		path string
		fn   func(*Report, string, []byte) error
	}{
		{dashboardPath, checkDashboard},
		{grafanaRulesPath, checkGrafanaRules},
		{promRulesPath, checkPromRules},
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
