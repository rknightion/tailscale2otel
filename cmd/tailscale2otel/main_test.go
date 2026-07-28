package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

const minimalValidYAML = `
tailscale:
  tailnet: "acme.org"
  auth:
    method: oauth
    oauth:
      client_id: "cid"
      client_secret: "csecret"
`

const warningYAML = `
tailscale:
  tailnet: "acme.org"
  auth:
    method: apikey
    apikey: "tskey-abc"
`

const invalidYAML = `
otlp:
  protocol: grpc
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

func TestRun_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != version {
		t.Errorf("stdout = %q, want %q", got, version)
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRun_ValidateValidConfig(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK") {
		t.Errorf("stdout = %q, want it to mention OK", stdout.String())
	}
}

func TestRun_ValidateInvalidConfig(t *testing.T) {
	path := writeTempConfig(t, invalidYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate", "-config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.String() == "" {
		t.Errorf("stderr should contain the validation error")
	}
}

func TestRun_ValidatePrintsWarnings(t *testing.T) {
	path := writeTempConfig(t, warningYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "WARN") {
		t.Errorf("stdout = %q, want it to contain a WARN advisory for apikey auth", stdout.String())
	}
}

func TestRun_ValidateMissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate", "-config", filepath.Join(t.TempDir(), "missing.yaml")}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stderr.String() == "" {
		t.Errorf("stderr should contain the load error")
	}
}

// TestRun_ValidateWarningsAsErrors is the #307 acceptance check for the
// strict deployment gate: a config with only advisory warnings (no hard
// errors) exits 0 by default but 1 under -warnings-as-errors.
func TestRun_ValidateWarningsAsErrors(t *testing.T) {
	path := writeTempConfig(t, warningYAML)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-validate", "-config", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("without -warnings-as-errors: exit code = %d, want 0", code)
	}

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"-validate", "-warnings-as-errors", "-config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("with -warnings-as-errors: exit code = %d, want 1; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

// TestRun_ValidateJSON asserts -validate -json emits a parseable JSON array
// of config.Diagnostic and includes the warning this fixture is known to
// trigger.
func TestRun_ValidateJSON(t *testing.T) {
	path := writeTempConfig(t, warningYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate", "-json", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var diags []config.Diagnostic
	if err := json.Unmarshal(stdout.Bytes(), &diags); err != nil {
		t.Fatalf("stdout is not a JSON array of Diagnostic: %v\nstdout=%s", err, stdout.String())
	}
	var sawWarning bool
	for _, d := range diags {
		if d.Severity == config.SeverityWarning {
			sawWarning = true
		}
	}
	if !sawWarning {
		t.Fatalf("expected at least one severity=warning diagnostic, got: %+v", diags)
	}
}

func TestRun_UnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

// The whole point of #307 is that a config with several independent problems
// reports all of them in one run, so repairing it is not a fix-run-fix loop.
// That only works if Load hands back the decoded config alongside the Validate
// error — otherwise there is nothing to call Diagnostics() on and the CLI
// falls back to the historical single error, doing less than it advertises.
func TestRun_ValidateJSONReportsEveryIndependentError(t *testing.T) {
	const y = "otlp:\n  protocol: bogus\ncheckpoint:\n  store: bogus\n"
	path := writeTempConfig(t, y)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate", "-json", "-config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for an invalid config; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var diags []config.Diagnostic
	if err := json.Unmarshal(stdout.Bytes(), &diags); err != nil {
		t.Fatalf("stdout is not a JSON array of Diagnostic: %v\nstdout=%s", err, stdout.String())
	}
	var sawProtocol, sawCheckpoint bool
	for _, d := range diags {
		if d.Severity != config.SeverityError {
			continue
		}
		if strings.Contains(d.Message, "otlp.protocol") || d.Path == "otlp.protocol" {
			sawProtocol = true
		}
		if strings.Contains(d.Message, "checkpoint.store") || d.Path == "checkpoint.store" {
			sawCheckpoint = true
		}
	}
	if !sawProtocol || !sawCheckpoint {
		t.Fatalf("want BOTH independent errors reported in one pass (otlp.protocol=%v checkpoint.store=%v); got %+v",
			sawProtocol, sawCheckpoint, diags)
	}
}
