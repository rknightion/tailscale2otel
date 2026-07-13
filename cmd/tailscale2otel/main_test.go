package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestRun_UnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
