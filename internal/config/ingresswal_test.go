package config_test

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

const (
	defaultIngressWALDirectory  = "/var/lib/tailscale2otel/ingress-wal"
	defaultIngressWALMaxBytes   = int64(268435456)
	defaultIngressWALMaxEntries = 10000
	maxIngressWALBodyBytes      = int64(64 << 20)
)

func TestIngressWALDefaults(t *testing.T) {
	got := config.Default().IngressWAL
	if got.Enabled {
		t.Error("IngressWAL.Enabled = true, want false")
	}
	if got.Directory != defaultIngressWALDirectory {
		t.Errorf("IngressWAL.Directory = %q, want %q", got.Directory, defaultIngressWALDirectory)
	}
	if got.MaxBytes != defaultIngressWALMaxBytes {
		t.Errorf("IngressWAL.MaxBytes = %d, want %d", got.MaxBytes, defaultIngressWALMaxBytes)
	}
	if got.MaxEntries != defaultIngressWALMaxEntries {
		t.Errorf("IngressWAL.MaxEntries = %d, want %d", got.MaxEntries, defaultIngressWALMaxEntries)
	}
	if got.Corruption != "fail" {
		t.Errorf("IngressWAL.Corruption = %q, want fail", got.Corruption)
	}
}

func TestLoadIngressWALFromYAML(t *testing.T) {
	const y = `
ingress_wal:
  enabled: true
  directory: /srv/tailscale2otel/wal
  max_bytes: 33554432
  max_entries: 1234
  corruption: fail
`
	cfg, err := config.Load(writeTemp(t, y))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.IngressWAL
	if !got.Enabled || got.Directory != "/srv/tailscale2otel/wal" ||
		got.MaxBytes != 33554432 || got.MaxEntries != 1234 || got.Corruption != "fail" {
		t.Errorf("IngressWAL = %+v, want enabled YAML values", got)
	}
}

func TestLoadIngressWALFromEnvironment(t *testing.T) {
	t.Setenv("TS2OTEL_INGRESS_WAL__ENABLED", "true")
	t.Setenv("TS2OTEL_INGRESS_WAL__DIRECTORY", "/srv/tailscale2otel/env-wal")
	t.Setenv("TS2OTEL_INGRESS_WAL__MAX_BYTES", "16777216")
	t.Setenv("TS2OTEL_INGRESS_WAL__MAX_ENTRIES", "4321")
	t.Setenv("TS2OTEL_INGRESS_WAL__CORRUPTION", "fail")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.IngressWAL
	if !got.Enabled || got.Directory != "/srv/tailscale2otel/env-wal" ||
		got.MaxBytes != 16777216 || got.MaxEntries != 4321 || got.Corruption != "fail" {
		t.Errorf("IngressWAL = %+v, want enabled environment values", got)
	}
}

func TestValidateIngressWALDisabledIsFullyInert(t *testing.T) {
	cfg := config.Default()
	cfg.IngressWAL = config.IngressWALConfig{
		Enabled:    false,
		Directory:  "../not-clean",
		MaxBytes:   math.MinInt64,
		MaxEntries: -1,
		Corruption: "discard",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate disabled ingress WAL: %v", err)
	}
}

func TestValidateIngressWALAllowsDrainOnly(t *testing.T) {
	cfg := config.Default()
	cfg.IngressWAL.Enabled = true
	cfg.Streaming.Enabled = false
	cfg.Webhook.Enabled = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate drain-only ingress WAL: %v", err)
	}
}

func TestValidateIngressWALAllowsHeadscale(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "headscale"
	cfg.Headscale.URL = "https://headscale.example.test"
	cfg.Headscale.APIKey = "headscale-api-key"
	cfg.IngressWAL.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate headscale ingress WAL: %v", err)
	}
}

func TestValidateIngressWALRejectsInvalidActiveSettings(t *testing.T) {
	tests := []struct {
		name string
		edit func(*config.IngressWALConfig)
		key  string
	}{
		{
			name: "empty directory",
			edit: func(c *config.IngressWALConfig) { c.Directory = "" },
			key:  "ingress_wal.directory",
		},
		{
			name: "relative directory",
			edit: func(c *config.IngressWALConfig) { c.Directory = "wal" },
			key:  "ingress_wal.directory",
		},
		{
			name: "non-clean directory",
			edit: func(c *config.IngressWALConfig) {
				c.Directory = "/var/lib/tailscale2otel/../ingress-wal"
			},
			key: "ingress_wal.directory",
		},
		{
			name: "filesystem root",
			edit: func(c *config.IngressWALConfig) { c.Directory = "/" },
			key:  "ingress_wal.directory",
		},
		{
			name: "zero max bytes",
			edit: func(c *config.IngressWALConfig) { c.MaxBytes = 0 },
			key:  "ingress_wal.max_bytes",
		},
		{
			name: "negative max bytes",
			edit: func(c *config.IngressWALConfig) { c.MaxBytes = -1 },
			key:  "ingress_wal.max_bytes",
		},
		{
			name: "max int64 bytes",
			edit: func(c *config.IngressWALConfig) { c.MaxBytes = math.MaxInt64 },
			key:  "ingress_wal.max_bytes",
		},
		{
			name: "zero max entries",
			edit: func(c *config.IngressWALConfig) { c.MaxEntries = 0 },
			key:  "ingress_wal.max_entries",
		},
		{
			name: "negative max entries",
			edit: func(c *config.IngressWALConfig) { c.MaxEntries = -1 },
			key:  "ingress_wal.max_entries",
		},
		{
			name: "empty corruption mode",
			edit: func(c *config.IngressWALConfig) { c.Corruption = "" },
			key:  "ingress_wal.corruption",
		},
		{
			name: "unsupported corruption mode",
			edit: func(c *config.IngressWALConfig) { c.Corruption = "skip" },
			key:  "ingress_wal.corruption",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.IngressWAL.Enabled = true
			tt.edit(&cfg.IngressWAL)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate: want an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error = %q, want offending key %q", err, tt.key)
			}
		})
	}
}

func TestValidateIngressWALReceiverBodyCap(t *testing.T) {
	const secretSentinel = "must-not-appear-in-errors"
	tests := []struct {
		name     string
		receiver string
		value    int64
	}{
		{name: "streaming zero", receiver: "streaming", value: 0},
		{name: "streaming negative", receiver: "streaming", value: -1},
		{name: "streaming over maximum", receiver: "streaming", value: maxIngressWALBodyBytes + 1},
		{name: "webhook zero", receiver: "webhook", value: 0},
		{name: "webhook negative", receiver: "webhook", value: -1},
		{name: "webhook over maximum", receiver: "webhook", value: maxIngressWALBodyBytes + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.IngressWAL.Enabled = true
			switch tt.receiver {
			case "streaming":
				cfg.Streaming.Enabled = true
				cfg.Streaming.MaxBodyBytes = tt.value
				cfg.Streaming.Token = secretSentinel
			case "webhook":
				cfg.Webhook.Enabled = true
				cfg.Webhook.MaxBodyBytes = tt.value
				cfg.Webhook.Secret = secretSentinel
			default:
				t.Fatalf("unknown receiver %q", tt.receiver)
			}

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate: want an error, got nil")
			}
			wantKey := tt.receiver + ".max_body_bytes"
			if !strings.Contains(err.Error(), wantKey) {
				t.Errorf("error = %q, want offending key %q", err, wantKey)
			}
			if !strings.Contains(err.Error(), "got "+strconv.FormatInt(tt.value, 10)) {
				t.Errorf("error = %q, want supplied value %d", err, tt.value)
			}
			if !strings.Contains(err.Error(), strconv.FormatInt(maxIngressWALBodyBytes, 10)) {
				t.Errorf("error = %q, want maximum %d", err, maxIngressWALBodyBytes)
			}
			if strings.Contains(err.Error(), secretSentinel) {
				t.Errorf("error = %q, must not expose receiver credentials", err)
			}
		})
	}
}

func TestValidateIngressWALAcceptsReceiverBodyCapBounds(t *testing.T) {
	for _, value := range []int64{1, maxIngressWALBodyBytes} {
		t.Run(strconv.FormatInt(value, 10), func(t *testing.T) {
			cfg := config.Default()
			cfg.IngressWAL.Enabled = true
			cfg.Streaming.Enabled = true
			cfg.Streaming.Token = "test-token"
			cfg.Streaming.MaxBodyBytes = value
			cfg.Webhook.Enabled = true
			cfg.Webhook.Secret = "test-secret"
			cfg.Webhook.MaxBodyBytes = value
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate body cap %d: %v", value, err)
			}
		})
	}
}
