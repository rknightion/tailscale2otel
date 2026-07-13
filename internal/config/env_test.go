package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

// TestLoadEnvOverridesDefaults: with no file, TS2OTEL_* variables populate the
// config on top of the built-in defaults. Covers the "__" nesting delimiter,
// single-underscore preservation within a name, and a few scalar types.
func TestLoadEnvOverridesDefaults(t *testing.T) {
	t.Setenv("TS2OTEL_TAILSCALE__TAILNET", "env-tailnet.org")
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID", "cid-from-env")
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET", "csecret-from-env")
	t.Setenv("TS2OTEL_OTLP__PROTOCOL", "stdout")
	t.Setenv("TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID", "999")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tailscale.Tailnet != "env-tailnet.org" {
		t.Errorf("Tailnet = %q, want env-tailnet.org", cfg.Tailscale.Tailnet)
	}
	if cfg.Tailscale.Auth.OAuth.ClientID != "cid-from-env" {
		t.Errorf("OAuth.ClientID = %q, want cid-from-env", cfg.Tailscale.Auth.OAuth.ClientID)
	}
	if cfg.Tailscale.Auth.OAuth.ClientSecret.Reveal() != "csecret-from-env" {
		t.Errorf("OAuth.ClientSecret = %q, want csecret-from-env", cfg.Tailscale.Auth.OAuth.ClientSecret.Reveal())
	}
	if cfg.OTLP.Protocol != "stdout" {
		t.Errorf("OTLP.Protocol = %q, want stdout", cfg.OTLP.Protocol)
	}
	if cfg.OTLP.GrafanaCloud.InstanceID != "999" {
		t.Errorf("GrafanaCloud.InstanceID = %q, want 999", cfg.OTLP.GrafanaCloud.InstanceID)
	}
	// An untouched field keeps its default.
	if cfg.OTLP.Endpoint != "https://otlp-gateway-prod-us-central-0.grafana.net/otlp" {
		t.Errorf("OTLP.Endpoint = %q, want default preserved", cfg.OTLP.Endpoint)
	}
}

// TestLoadEnvOverridesFile: the environment layer wins over a value set in the
// file (the documented "secrets live in env, file holds literals" model).
func TestLoadEnvOverridesFile(t *testing.T) {
	const y = `
otlp:
  protocol: http
  endpoint: "https://file.example/otlp"
`
	t.Setenv("TS2OTEL_OTLP__ENDPOINT", "https://env.example/otlp")
	cfg, err := config.Load(writeTemp(t, y))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OTLP.Endpoint != "https://env.example/otlp" {
		t.Errorf("OTLP.Endpoint = %q, want the env override to win", cfg.OTLP.Endpoint)
	}
	// A file-only field is still applied.
	if cfg.OTLP.Protocol != "http" {
		t.Errorf("OTLP.Protocol = %q, want http from file", cfg.OTLP.Protocol)
	}
}

// TestLoadEnvDurationAndBool: env values are strings; durations parse through the
// decode hook and "false" overrides a default-true bool.
func TestLoadEnvDurationAndBool(t *testing.T) {
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL", "30s")
	t.Setenv("TS2OTEL_COLLECTORS__DEVICES__ENABLED", "false")
	t.Setenv("TS2OTEL_CARDINALITY__METRIC_LIMIT", "250")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Collectors.Flowlogs.Interval.D() != 30*time.Second {
		t.Errorf("Flowlogs.Interval = %v, want 30s from env", cfg.Collectors.Flowlogs.Interval.D())
	}
	if cfg.Collectors.Devices.Enabled {
		t.Errorf("Devices.Enabled = true, want false (env override of default true)")
	}
	if cfg.Cardinality.MetricLimit != 250 {
		t.Errorf("Cardinality.MetricLimit = %d, want 250 from env", cfg.Cardinality.MetricLimit)
	}
}

// TestLoadEnvListSplitsOnComma: the scalar []string fields accept a
// comma-separated env value.
func TestLoadEnvListSplitsOnComma(t *testing.T) {
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES", "all:read,devices:read")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Tailscale.Auth.OAuth.Scopes
	if len(got) != 2 || got[0] != "all:read" || got[1] != "devices:read" {
		t.Errorf("OAuth.Scopes = %v, want [all:read devices:read]", got)
	}
}

// TestLoadWarnsOnUnknownEnvVar: a TS2OTEL_* variable that does not map to any
// config key is a likely typo and surfaces as a Warning.
func TestLoadWarnsOnUnknownEnvVar(t *testing.T) {
	t.Setenv("TS2OTEL_OTLP__ENDPONT", "https://typo.example/otlp") // ENDPONT, not ENDPOINT
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var found bool
	for _, w := range cfg.Warnings() {
		if strings.Contains(w, "TS2OTEL_OTLP__ENDPONT") {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings() = %v, want one naming the unknown env var", cfg.Warnings())
	}
}

// TestLoadRejectsEnvVarIndexingIntoNodeMetricsTargets: collectors.node_metrics
// .targets is a []struct, documented as file-only (env.go). An env var that
// indexes into it (e.g. TS2OTEL_COLLECTORS__NODE_METRICS__TARGETS__0__URL)
// cannot be expressed as a flat env value — mapstructure would otherwise
// silently decode it into a slice holding a mostly-empty struct, dropping the
// URL the operator actually set. Load must reject this outright with an
// actionable error naming both the offending variable and the file-only key,
// regardless of whether node_metrics itself is enabled (issue #79).
func TestLoadRejectsEnvVarIndexingIntoNodeMetricsTargets(t *testing.T) {
	for _, enabled := range []string{"true", "false"} {
		t.Run("node_metrics_enabled="+enabled, func(t *testing.T) {
			t.Setenv("TS2OTEL_COLLECTORS__NODE_METRICS__ENABLED", enabled)
			t.Setenv("TS2OTEL_COLLECTORS__NODE_METRICS__TARGETS__0__URL", "http://tailscaled:5252/metrics")

			_, err := config.Load("")
			if err == nil {
				t.Fatal("Load: want an error, got nil")
			}
			if !strings.Contains(err.Error(), "TS2OTEL_COLLECTORS__NODE_METRICS__TARGETS__0__URL") {
				t.Errorf("error = %q, want it to name the offending env var", err)
			}
			if !strings.Contains(err.Error(), "collectors.node_metrics.targets") {
				t.Errorf("error = %q, want it to name the file-only config key", err)
			}
			if strings.Contains(err.Error(), "targets[0].url is required") {
				t.Errorf("error = %q, must not surface the confusing decode-artifact validate error instead", err)
			}
		})
	}
}

// TestLoadRejectsEnvVarIndexingIntoTailnets covers the other list-of-structs
// config key named in issue #79 (tailnets), confirming the rejection is
// general rather than hardcoded to node_metrics.targets alone.
func TestLoadRejectsEnvVarIndexingIntoTailnets(t *testing.T) {
	t.Setenv("TS2OTEL_TAILNETS__0__NAME", "acme.example.com")

	_, err := config.Load("")
	if err == nil {
		t.Fatal("Load: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "TS2OTEL_TAILNETS__0__NAME") {
		t.Errorf("error = %q, want it to name the offending env var", err)
	}
	if !strings.Contains(err.Error(), "tailnets") {
		t.Errorf("error = %q, want it to name the file-only config key", err)
	}
}
