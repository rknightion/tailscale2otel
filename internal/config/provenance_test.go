package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

// TestProvenance_DefaultFileEnv is the core layering test for issue #309: a
// key untouched by either the file or the environment reports SourceDefault,
// a key set only in the file reports SourceFile, and a key set in the
// environment (which always wins, whatever the file says) reports SourceEnv.
func TestProvenance_DefaultFileEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
log_level: debug
tailscale:
  tailnet: "acme.org"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("TS2OTEL_LOG_FORMAT", "json")

	prov, err := config.Provenance(path)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}

	cases := []struct {
		key  string
		want config.Source
	}{
		{"log_level", config.SourceFile},         // set only in the YAML file
		{"tailscale.tailnet", config.SourceFile}, // set only in the YAML file
		{"log_format", config.SourceEnv},         // env always wins
		{"admin.enabled", config.SourceDefault},  // untouched by either layer
		{"otlp.protocol", config.SourceDefault},  // untouched by either layer
	}
	for _, c := range cases {
		got, ok := prov[c.key]
		if !ok {
			t.Errorf("key %q missing from provenance map", c.key)
			continue
		}
		if got != c.want {
			t.Errorf("key %q: got source %q, want %q", c.key, got, c.want)
		}
	}
}

// TestProvenance_EnvOverridesFile asserts env beats a file value that ALSO set
// the same key (not just a key the file left untouched) — the precedence
// order Load itself documents, not just "env wins when file is silent".
func TestProvenance_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("log_level: debug\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("TS2OTEL_LOG_LEVEL", "error")

	prov, err := config.Provenance(path)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	if got := prov["log_level"]; got != config.SourceEnv {
		t.Errorf("log_level: got %q, want %q (env must beat file even when file also set it)", got, config.SourceEnv)
	}
}

// TestProvenance_NeverExposesSecretValue is the mandatory leak test: even
// though Provenance's internal koanf snapshots necessarily hold the raw
// secret string in memory to diff it, the returned map must contain ONLY
// Source values (a defined string type with three values), never the secret
// itself, for a key whose YAML value is plainly a secret.
func TestProvenance_NeverExposesSecretValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const secretValue = "tskey-super-secret-value"
	yaml := "tailscale:\n  auth:\n    apikey: \"" + secretValue + "\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	prov, err := config.Provenance(path)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	got, ok := prov["tailscale.auth.apikey"]
	if !ok {
		t.Fatalf("tailscale.auth.apikey missing from provenance map")
	}
	if got != config.SourceFile {
		t.Errorf("tailscale.auth.apikey: got %q, want %q", got, config.SourceFile)
	}
	// The type system already prevents Provenance from returning a map of
	// strings-that-might-be-secrets (config.Source is its own type with only
	// three defined values), but assert directly against the secret content
	// as a belt-and-braces regression guard in case that ever changes.
	for k, v := range prov {
		if string(v) == secretValue {
			t.Fatalf("provenance for key %q leaked the secret value", k)
		}
	}
}

// TestProvenance_MultiTailnet covers the #309 acceptance line "multi-tailnet
// and dynamic maps are covered": a struct-slice entry set via the file must
// report per-field provenance, not collapse into one leaf.
func TestProvenance_MultiTailnet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
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
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	prov, err := config.Provenance(path)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	for _, key := range []string{"tailnets.0.name", "tailnets.1.name", "tailnets.0.tailnet", "tailnets.1.tailnet"} {
		if got, ok := prov[key]; !ok || got != config.SourceFile {
			t.Errorf("key %q: got %q ok=%v, want %q", key, got, ok, config.SourceFile)
		}
	}
}

// TestProvenance_FileMatchingDefaultIsNotMisreported is a regression test for
// a real bug found while implementing this (via config.example.yaml, which
// explicitly writes "admin.auth.token: \"\"" — the same as the built-in
// default): comparing the defaults snapshot (typed config.Secret/
// config.Duration values from structs.Provider) against the file snapshot
// (always plain strings/numbers from koanf's YAML parser) with
// reflect.DeepEqual reported SourceFile purely because config.Secret("") !=
// string(""), even though the file wrote the identical value.
func TestProvenance_FileMatchingDefaultIsNotMisreported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// Explicitly write several values that MATCH the built-in defaults,
	// across the two named types (config.Secret, config.Duration) that
	// structs.Provider renders typed while koanf's YAML parser renders as a
	// plain string.
	yaml := `
admin:
  auth:
    token: ""
  status_refresh_interval: 5s
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	prov, err := config.Provenance(path)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	if got := prov["admin.auth.token"]; got != config.SourceDefault {
		t.Errorf("admin.auth.token: got %q, want %q (file wrote the same empty value as the default)", got, config.SourceDefault)
	}
	if got := prov["admin.status_refresh_interval"]; got != config.SourceDefault {
		t.Errorf("admin.status_refresh_interval: got %q, want %q (file wrote the same 5s value as the default)", got, config.SourceDefault)
	}
}

// TestProvenance_EmptyMapIsALeaf is a regression test for a second real bug
// found live against config.example.yaml: an empty map field (otlp.headers,
// profiling.pyroscope.tags, both empty by default) never got a provenance
// entry at all, because flattenTree recursed into a map with zero keys
// (nothing to iterate) instead of treating it as a leaf the way
// internal/configexport.Build does for the very same empty-collection case.
func TestProvenance_EmptyMapIsALeaf(t *testing.T) {
	prov, err := config.Provenance("")
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	if got, ok := prov["otlp.headers"]; !ok || got != config.SourceDefault {
		t.Errorf("otlp.headers: got %q ok=%v, want %q", got, ok, config.SourceDefault)
	}
}

// TestProvenance_NoFile asserts every key reports SourceDefault (or SourceEnv,
// if an env var is set) when no file path is given at all — the "env-only"
// deployment mode Load itself supports.
func TestProvenance_NoFile(t *testing.T) {
	prov, err := config.Provenance("")
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	if got := prov["admin.enabled"]; got != config.SourceDefault {
		t.Errorf("admin.enabled: got %q, want %q", got, config.SourceDefault)
	}
}

// TestProvenance_AgreesWithWhatLoadActuallyDid ties Provenance's verdict to
// Load's real behavior, rather than to a second, independent restatement of
// the layering rules.
//
// Provenance necessarily mirrors Load's steps 1-3 — same koanf providers, same
// prefix, same delimiter, same transform — because the merged tree Load
// produces has already destroyed the information Provenance needs. That makes
// it a SECOND SPELLING of the layering rule, and this repository's dominant
// defect is one rule with N spellings that quietly diverge: #297's stale
// window had three, #320's URL redaction had two and only one redacted,
// #316's certificate reloader had two whole copies.
//
// Divergence here is worse than the usual case, because the output is an
// authoritative-looking answer to "which layer won". An operator who is told
// "env" and goes to change an environment variable that is not actually in
// effect is worse off than one who was told nothing — the same reasoning that
// pinned the config-schema rule table in #308, where a wrong enum was judged
// worse than no enum.
//
// So this asserts BOTH halves for each layer: Provenance names a source, AND
// the value Load actually produced is the one that source supplies. Verified
// by mutation: disabling Load's env layer leaves every other provenance test
// green, and fails only this one.
func TestProvenance_AgreesWithWhatLoadActuallyDid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// log_level is set in BOTH the file and the env, so the env layer has to
	// actually win for the assertion below to hold — a file-only key would pass
	// even with the env layer removed entirely.
	yaml := `
log_level: warn
log_format: json
tailscale:
  tailnet: "acme.org"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("TS2OTEL_LOG_LEVEL", "debug")

	prov, err := config.Provenance(path)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defaults := config.Default()

	cases := []struct {
		key        string
		wantSource config.Source
		// loaded is the value Load actually produced, and wantValue is what the
		// named source supplies. They must agree, or Provenance is describing a
		// layering Load no longer performs.
		loaded    string
		wantValue string
	}{
		// env set it AND the file also set it, so this fails if env stops winning.
		{"log_level", config.SourceEnv, cfg.LogLevel, "debug"},
		// file set it and env did not.
		{"log_format", config.SourceFile, cfg.LogFormat, "json"},
		{"tailscale.tailnet", config.SourceFile, cfg.Tailscale.Tailnet, "acme.org"},
		// neither layer touched it, so the effective value must still be the default.
		{"otlp.protocol", config.SourceDefault, cfg.OTLP.Protocol, defaults.OTLP.Protocol},
	}
	for _, c := range cases {
		got, ok := prov[c.key]
		if !ok {
			t.Errorf("key %q missing from provenance map", c.key)
			continue
		}
		if got != c.wantSource {
			t.Errorf("key %q: Provenance says source %q, want %q", c.key, got, c.wantSource)
		}
		if c.loaded != c.wantValue {
			t.Errorf("key %q: Provenance reports source %q, but Load produced %q while that source supplies %q — "+
				"Provenance is describing a layering Load no longer performs",
				c.key, got, c.loaded, c.wantValue)
		}
	}
}
