package config_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestBlankEnvAliasPreservesHeadscaleExplicitChoice(t *testing.T) {
	path := writeTemp(t, "provider: headscale\nheadscale:\n  url: https://headscale.invalid\n  api_key: synthetic\n")
	const canonical = "TS2OTEL_VERSION_CHECKS__DEVICES__ENABLED"
	// Dots and double underscores normalize to the same key but remain
	// distinct environment names on case-insensitive operating systems.
	const alias = "TS2OTEL_version_checks.devices.enabled"
	t.Setenv(canonical, "true")
	t.Setenv(alias, " ")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.VersionChecks.Devices.Enabled {
		t.Error("blank alias suppressed the nonblank explicit Headscale choice")
	}
	warnings := strings.Join(cfg.Warnings(), "\n")
	if !strings.Contains(warnings, alias) || strings.Contains(warnings, canonical) {
		t.Error("advisory must name the skipped alias, not the accepted variable")
	}
	if strings.Contains(warnings, "the value from the config file or built-in default stands") {
		t.Error("advisory incorrectly attributes the accepted alias value to a lower layer")
	}
	t.Setenv(canonical, "")
	t.Setenv(alias, "true")
	cfg, err = config.Load(path)
	if err != nil || !cfg.VersionChecks.Devices.Enabled {
		t.Fatalf("nonblank dotted alias must select the same Headscale key: %v", err)
	}
	t.Setenv(alias, "false")
	path = writeTemp(t, "provider: headscale\nheadscale:\n  url: https://headscale.invalid\n  api_key: synthetic\nversion_checks:\n  devices:\n    enabled: true\n")
	cfg, err = config.Load(path)
	if err != nil || cfg.VersionChecks.Devices.Enabled {
		t.Fatalf("nonblank dotted alias must preserve explicit false: %v", err)
	}
	warnings = strings.Join(cfg.Warnings(), "\n")
	if !strings.Contains(warnings, canonical) || strings.Contains(warnings, alias) || strings.Contains(warnings, "the value from the config file or built-in default stands") {
		t.Error("advisory must name the blank canonical variable without attributing the value to a lower layer")
	}
	provenance, err := config.Provenance(path)
	if err != nil || provenance["version_checks.devices.enabled"] != config.SourceEnv {
		t.Errorf("explicit false provenance = %q, error = %v", provenance["version_checks.devices.enabled"], err)
	}
	// Warnings describe the load, not a later environment change.
	t.Setenv(alias, "")
	if got := strings.Join(cfg.Warnings(), "\n"); got != warnings {
		t.Error("warnings changed after the environment changed")
	}
	// An accepted false remains an override even when equal to the file value.
	t.Setenv(alias, "false")
	path = writeTemp(t, "provider: headscale\nheadscale:\n  url: https://headscale.invalid\n  api_key: synthetic\nversion_checks:\n  devices:\n    enabled: false\n")
	cfg, err = config.Load(path)
	if err != nil || cfg.VersionChecks.Devices.Enabled {
		t.Fatalf("load explicit false equal to file: %v", err)
	}
	if got := strings.Join(cfg.Warnings(), "\n"); !strings.Contains(got, canonical) || strings.Contains(got, "the value from the config file or built-in default stands") {
		t.Error("accepted false equal to file must not claim the lower layer supplies the value")
	}
}

func TestBlankEnvForTypedKeyIsIgnoredWithAdvisory(t *testing.T) {
	t.Setenv("TS2OTEL_ADMIN__ENABLED", "  ")
	t.Setenv("TS2OTEL_TAILSCALE__HTTP__RETRY__MAX_ATTEMPTS", "")
	t.Setenv("TS2OTEL_TAILSCALE__HTTP__RATE_LIMIT", " ")
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID", "")
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES", "")
	path := writeTemp(t, `
provider: headscale
headscale:
  url: https://headscale.invalid
  api_key: synthetic
admin:
  enabled: true
tailscale:
  http:
    retry:
      max_attempts: 7
    rate_limit: 2.5
  auth:
    oauth:
      client_id: synthetic
      scopes: [devices:read]
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Admin.Enabled || cfg.Tailscale.HTTP.Retry.MaxAttempts != 7 || cfg.Tailscale.HTTP.RateLimit != 2.5 {
		t.Errorf("blank scalars overrode file values")
	}
	if cfg.Tailscale.Auth.OAuth.ClientID != "" || len(cfg.Tailscale.Auth.OAuth.Scopes) != 0 {
		t.Error("blank string or list did not clear file value")
	}
	warnings := strings.Join(cfg.Warnings(), "\n")
	for _, name := range []string{"TS2OTEL_ADMIN__ENABLED", "TS2OTEL_TAILSCALE__HTTP__RETRY__MAX_ATTEMPTS", "TS2OTEL_TAILSCALE__HTTP__RATE_LIMIT"} {
		if !strings.Contains(warnings, name+" is set but empty and was ignored for a boolean or numeric key; the value from the config file or built-in default stands") {
			t.Errorf("missing lower-layer advisory for %s", name)
		}
	}
	for _, name := range []string{"TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID", "TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES"} {
		if strings.Contains(warnings, name) {
			t.Errorf("unexpected advisory for %s", name)
		}
	}
	provenance, err := config.Provenance(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]config.Source{"admin.enabled": config.SourceDefault, "tailscale.http.retry.max_attempts": config.SourceFile, "tailscale.http.rate_limit": config.SourceFile} {
		if provenance[key] != want {
			t.Errorf("%s provenance = %q, want %q", key, provenance[key], want)
		}
	}
	t.Setenv("TS2OTEL_VERSION_CHECKS__DEVICES__ENABLED", " ")
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VersionChecks.Devices.Enabled {
		t.Error("blank env counted as explicit Headscale choice")
	}
	explicit := writeTemp(t, "provider: headscale\nheadscale:\n  url: https://headscale.invalid\n  api_key: synthetic\nversion_checks:\n  devices:\n    enabled: true\n")
	cfg, err = config.Load(explicit)
	if err != nil || !cfg.VersionChecks.Devices.Enabled {
		t.Errorf("blank env must preserve explicit file choice: %v", err)
	}
	t.Setenv("TS2OTEL_TAILSCALE__HTTP__TIMEOUT", "")
	if _, err := config.Load(path); err == nil {
		t.Error("blank duration accepted")
	}
	t.Setenv("TS2OTEL_TAILSCALE__HTTP__TIMEOUT", "1s")
	t.Setenv("TS2OTEL_ADMIN__ENABLED", "not-a-bool")
	if _, err := config.Load(path); err == nil {
		t.Error("invalid boolean accepted")
	}
}
