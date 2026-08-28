package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This tool's whole purpose is to catch cross-field configuration faults that the
// chart's JSON Schema draft-07 cannot express, and CI trusts its exit code. These
// tests exist so that trust is checked: before #437 the module had no tests at
// all, so a `go test` leg over it would have passed vacuously.

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A minimal valid config: the loader fills everything else from defaults.
const validConfig = `
tailscale:
  tailnet: example.com
  auth:
    method: apikey
    apikey: k
`

func TestCheck_AcceptsAValidConfig(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := check([]string{write(t, "ok.yaml", validConfig)}, &out, &errOut); got != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", got, errOut.String())
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("stdout = %q, want an OK line", out.String())
	}
}

func TestCheck_AcceptsNodeMetricsDiscoveryPortOverrides(t *testing.T) {
	const y = validConfig + `
collectors:
  node_metrics:
    enabled: true
    discovery:
      enabled: true
      port_overrides:
        "tag:k8s-operator": [9002]
`
	var out, errOut bytes.Buffer
	if got := check([]string{write(t, "node-metrics.yaml", y)}, &out, &errOut); got != 0 {
		t.Fatalf("exit = %d, want 0; stdout: %s; stderr: %s", got, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("stdout = %q, want an OK line", out.String())
	}
}

// The cross-field rule is the reason this tool exists rather than relying on the
// schema: poll+stream on one log type double-counts, and no per-field schema can
// see it. If this stops failing, the chart-rendered config check is worthless.
func TestCheck_RejectsACrossFieldFault(t *testing.T) {
	const streamWithoutReceiver = validConfig + `
collectors:
  flowlogs:
    enabled: true
    source: stream
`
	var out, errOut bytes.Buffer
	got := check([]string{write(t, "bad.yaml", streamWithoutReceiver)}, &out, &errOut)
	if got != 1 {
		t.Fatalf("exit = %d, want 1 — source=stream with streaming disabled has no ingestion path "+
			"at all, so flow logs are silently never collected", got)
	}
	if !strings.Contains(errOut.String(), "FAIL") {
		t.Errorf("stderr = %q, want a FAIL line naming the file", errOut.String())
	}
}

func TestCheck_MissingFileFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := check([]string{filepath.Join(t.TempDir(), "absent.yaml")}, &out, &errOut); got != 1 {
		t.Errorf("exit = %d, want 1 for an unreadable path", got)
	}
}

// Exit 2 distinguishes "you invoked me wrongly" from "the config is bad". CI
// treats any non-zero as failure, but conflating them would make a broken
// workflow step look like a config regression.
func TestCheck_NoArgumentsIsAUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := check(nil, &out, &errOut); got != 2 {
		t.Errorf("exit = %d, want 2 for no arguments", got)
	}
	if !strings.Contains(errOut.String(), "usage:") {
		t.Errorf("stderr = %q, want usage text", errOut.String())
	}
}

// Every path is checked even after one fails, so one broken file does not hide
// the state of the rest.
func TestCheck_ReportsEveryPathNotJustTheFirstFailure(t *testing.T) {
	bad := write(t, "bad.yaml", "tailscale: {auth: {method: nonsense}}\n")
	good := write(t, "good.yaml", validConfig)

	var out, errOut bytes.Buffer
	if got := check([]string{bad, good}, &out, &errOut); got != 1 {
		t.Fatalf("exit = %d, want 1", got)
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("the valid file after a failure was not reported: stdout %q", out.String())
	}
}
