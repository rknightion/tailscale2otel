package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRun_PrintEffectiveConfigJSON is the #309 acceptance check: every key
// appears with its effective, redacted value, as one machine-readable JSON
// document on stdout.
func TestRun_PrintEffectiveConfigJSON(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-print-effective-config", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	var entries []effectiveConfigEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\nstdout=%s", err, stdout.String())
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}

	byKey := map[string]effectiveConfigEntry{}
	for _, e := range entries {
		byKey[e.Key] = e
	}
	got, ok := byKey["tailscale.tailnet"]
	if !ok {
		t.Fatal("tailscale.tailnet missing from output")
	}
	if got.Value != "acme.org" {
		t.Errorf("tailscale.tailnet value = %v, want acme.org", got.Value)
	}

	// The secret must never appear as a value anywhere in the raw output.
	if strings.Contains(stdout.String(), "csecret") {
		t.Errorf("secret leaked into -print-effective-config output: %s", stdout.String())
	}
	secretEntry, ok := byKey["tailscale.auth.oauth.client_secret"]
	if !ok {
		t.Fatal("tailscale.auth.oauth.client_secret missing from output")
	}
	if !secretEntry.Secret || !secretEntry.Set {
		t.Errorf("client_secret entry = %+v, want Secret=true Set=true", secretEntry)
	}
	if secretEntry.Value != nil {
		t.Errorf("client_secret entry carries a Value (%v), want none", secretEntry.Value)
	}

	// Provenance must be absent (empty string, omitted from JSON) unless
	// -print-effective-config-provenance was passed.
	if got.Provenance != "" {
		t.Errorf("tailscale.tailnet provenance = %q, want empty (provenance not requested)", got.Provenance)
	}
}

// TestRun_PrintEffectiveConfigProvenance asserts -print-effective-config
// -provenance adds the winning-layer report without ever adding value
// content for a secret field.
func TestRun_PrintEffectiveConfigProvenance(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-print-effective-config", "-print-effective-config-provenance", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var entries []effectiveConfigEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\nstdout=%s", err, stdout.String())
	}
	byKey := map[string]effectiveConfigEntry{}
	for _, e := range entries {
		byKey[e.Key] = e
	}

	if got := byKey["tailscale.tailnet"].Provenance; got != "file" {
		t.Errorf("tailscale.tailnet provenance = %q, want %q", got, "file")
	}
	if got := byKey["admin.enabled"].Provenance; got != "default" {
		t.Errorf("admin.enabled provenance = %q, want %q", got, "default")
	}
	// The secret's provenance is reported, but never its value.
	if got := byKey["tailscale.auth.oauth.client_secret"].Provenance; got != "file" {
		t.Errorf("client_secret provenance = %q, want %q", got, "file")
	}
	if strings.Contains(stdout.String(), "csecret") {
		t.Errorf("secret leaked with -print-effective-config-provenance: %s", stdout.String())
	}
}

// TestRun_PrintEffectiveConfigYAML asserts the -print-effective-config-format
// yaml path produces valid, parseable YAML with the same content.
func TestRun_PrintEffectiveConfigYAML(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-print-effective-config", "-print-effective-config-format", "yaml", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var entries []effectiveConfigEntry
	if err := yaml.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("stdout is not valid YAML: %v\nstdout=%s", err, stdout.String())
	}
	found := false
	for _, e := range entries {
		if e.Key == "tailscale.tailnet" && e.Value == "acme.org" {
			found = true
		}
	}
	if !found {
		t.Errorf("tailscale.tailnet=acme.org not found in YAML output:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "csecret") {
		t.Errorf("secret leaked into YAML output: %s", stdout.String())
	}
}

// TestRun_PrintEffectiveConfigDeterministic guards the #309 acceptance line
// "output is deterministic": two runs against the same config must produce
// byte-identical stdout.
func TestRun_PrintEffectiveConfigDeterministic(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML)

	var a, b bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"-print-effective-config", "-config", path}, &a, &stderr); code != 0 {
		t.Fatalf("run 1: exit code = %d", code)
	}
	if code := run([]string{"-print-effective-config", "-config", path}, &b, &stderr); code != 0 {
		t.Fatalf("run 2: exit code = %d", code)
	}
	if a.String() != b.String() {
		t.Fatalf("output not deterministic across runs:\na=%s\nb=%s", a.String(), b.String())
	}
}

// TestRun_PrintEffectiveConfigMultiTailnet is the #309 acceptance line
// "multi-tailnet and dynamic maps are covered".
func TestRun_PrintEffectiveConfigMultiTailnet(t *testing.T) {
	const y = `
tailnets:
  - name: "one"
    tailnet: "one.example.com"
    auth:
      method: apikey
      apikey: "tskey-one"
  - name: "two"
    tailnet: "two.example.com"
    auth:
      method: apikey
      apikey: "tskey-two"
`
	path := writeTempConfig(t, y)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-print-effective-config", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var entries []effectiveConfigEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("stdout is not a JSON array: %v", err)
	}
	byKey := map[string]effectiveConfigEntry{}
	for _, e := range entries {
		byKey[e.Key] = e
	}
	for _, key := range []string{"tailnets.0.name", "tailnets.1.name"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("key %q missing — multi-tailnet entries must be covered individually", key)
		}
	}
	if strings.Contains(stdout.String(), "tskey-one") || strings.Contains(stdout.String(), "tskey-two") {
		t.Errorf("multi-tailnet secrets leaked: %s", stdout.String())
	}
}

// TestRun_PrintEffectiveConfigInvalidFormat rejects an unknown
// -print-effective-config-format value rather than silently defaulting.
func TestRun_PrintEffectiveConfigInvalidFormat(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-print-effective-config", "-print-effective-config-format", "toml", "-config", path}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.String() == "" {
		t.Error("expected an error on stderr for an unknown format")
	}
}

// TestRun_PrintEffectiveConfigLoadError asserts a bad config still fails
// loudly (on stderr, non-zero exit) rather than printing a partial/garbage
// effective-config document.
func TestRun_PrintEffectiveConfigLoadError(t *testing.T) {
	path := writeTempConfig(t, invalidYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-print-effective-config", "-config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stderr.String() == "" {
		t.Error("expected an error on stderr")
	}
	if stdout.String() != "" {
		t.Errorf("stdout should be empty on error, got %q", stdout.String())
	}
}
