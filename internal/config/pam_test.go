package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestPAMDefaultsAreOptInAndBounded(t *testing.T) {
	cfg := config.Default()
	if cfg.PAM.APIURL != "https://api.border0.com/api/v1" || cfg.PAM.Token.Reveal() != "" {
		t.Fatalf("PAM defaults = APIURL %q token %q", cfg.PAM.APIURL, cfg.PAM.Token)
	}
	if cfg.Collectors.PAM.Enabled {
		t.Fatal("PAM collector defaults enabled")
	}
	if got := cfg.Collectors.PAM.Interval.D(); got != 10*time.Minute {
		t.Fatalf("PAM interval = %v, want 10m", got)
	}
	if got := cfg.Collectors.PAM.SessionsInterval.D(); got != time.Minute {
		t.Fatalf("PAM sessions interval = %v, want 1m", got)
	}
	if cfg.Collectors.PAM.SnapshotEnabled {
		t.Fatal("PAM snapshot defaults enabled")
	}
	if got := cfg.Collectors.PAM.SnapshotHeartbeat.D(); got != 24*time.Hour {
		t.Fatalf("PAM snapshot heartbeat = %v, want 24h", got)
	}
	if got := cfg.Collectors.PAM.SnapshotBodyBytes; got != 32*1024 {
		t.Fatalf("PAM snapshot body bytes = %d, want 32768", got)
	}
}

func TestPAMTokenLoadsFromFrozenEnvironmentKey(t *testing.T) {
	t.Setenv("TS2OTEL_PAM__TOKEN", "fixture-token")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PAM.Token.Reveal(); got != "fixture-token" {
		t.Fatalf("PAM token = %q, want fixture-token", got)
	}
}

func TestPAMEnabledRequiresCredentialAndValidIntervals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"token", func(c *config.Config) { c.PAM.Token = "" }, "pam.token"},
		{"api url", func(c *config.Config) { c.PAM.APIURL = "not-a-url" }, "pam.api_url"},
		{"api url invalid port", func(c *config.Config) { c.PAM.APIURL = "https://example.invalid:notaport/api/v1" }, "pam.api_url"},
		{"inventory interval", func(c *config.Config) { c.Collectors.PAM.Interval = 0 }, "collectors.pam.interval"},
		{"sessions interval", func(c *config.Config) { c.Collectors.PAM.SessionsInterval = 0 }, "collectors.pam.sessions_interval"},
		{"snapshot heartbeat", func(c *config.Config) { c.Collectors.PAM.SnapshotHeartbeat = 0 }, "collectors.pam.snapshot_heartbeat"},
		{"snapshot body", func(c *config.Config) { c.Collectors.PAM.SnapshotBodyBytes = 0 }, "collectors.pam.snapshot_body_bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Collectors.PAM.Enabled = true
			cfg.PAM.Token = "fixture-token"
			test.mutate(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}
