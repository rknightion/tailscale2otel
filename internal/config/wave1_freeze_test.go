package config_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

func TestWave1ConfigDefaultsPreserveExistingCapacities(t *testing.T) {
	cfg := config.Default()
	if got := cfg.Collectors.Devices.ExpiryLogMode; got != "daily" {
		t.Errorf("devices expiry_log_mode = %q, want daily", got)
	}
	if got := cfg.Collectors.Keys.ExpiryLogMode; got != "daily" {
		t.Errorf("keys expiry_log_mode = %q, want daily", got)
	}
	if got := cfg.Collectors.Devices.AttributeKeyLimit; got != 200 {
		t.Errorf("attribute_key_limit = %d, want 200", got)
	}
	if got := cfg.Collectors.Devices.AttributeValueLimit; got != 50 {
		t.Errorf("attribute_value_limit = %d, want 50", got)
	}
	if got := cfg.Collectors.Flowlogs.DedupCapacity; got != 16384 {
		t.Errorf("flowlogs dedup_capacity = %d, want 16384", got)
	}
	if got := cfg.Collectors.Auditlogs.DedupCapacity; got != 4096 {
		t.Errorf("auditlogs dedup_capacity = %d, want 4096", got)
	}
	if got := cfg.Collectors.Flowlogs.ObjectStore.MaxSeenKeys; got != 5000 {
		t.Errorf("objectstore max_seen_keys = %d, want 5000", got)
	}
}

func TestValidateHeadscaleIPPrefixes(t *testing.T) {
	valid := []string{"10.100.0.0/16", "172.20.0.0/16", "192.168.44.0/24", "100.64.0.0/10", "fd00:dead:beef::/48"}
	for _, prefix := range valid {
		t.Run("valid_"+strings.NewReplacer("/", "_", ":", "_").Replace(prefix), func(t *testing.T) {
			cfg := validHeadscaleConfig()
			cfg.Headscale.IPPrefixes = []string{prefix}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate(%q) = %v, want nil", prefix, err)
			}
		})
	}

	invalid := []string{
		"not-a-prefix",
		"10.100.0.1/16",
		"0.0.0.0/0",
		"8.8.8.0/24",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"224.0.0.0/4",
		"::/0",
		"::1/128",
		"fe80::/10",
	}
	for _, prefix := range invalid {
		t.Run("invalid_"+strings.NewReplacer("/", "_", ":", "_").Replace(prefix), func(t *testing.T) {
			cfg := validHeadscaleConfig()
			cfg.Headscale.IPPrefixes = []string{prefix}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "headscale.ip_prefixes") {
				t.Fatalf("Validate(%q) = %v, want error naming headscale.ip_prefixes", prefix, err)
			}
		})
	}

	cfg := config.Default()
	cfg.Headscale.IPPrefixes = []string{"10.100.0.0/16"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "provider=headscale") {
		t.Fatalf("tailscale provider with headscale prefixes: Validate = %v, want provider error", err)
	}
}

func TestValidateWave1EnumsAndCapacities(t *testing.T) {
	tests := []struct {
		name string
		set  func(*config.Config)
		want string
	}{
		{"devices expiry mode", func(c *config.Config) { c.Collectors.Devices.ExpiryLogMode = "sometimes" }, "collectors.devices.expiry_log_mode"},
		{"keys expiry mode", func(c *config.Config) { c.Collectors.Keys.ExpiryLogMode = "sometimes" }, "collectors.keys.expiry_log_mode"},
		{"flow dedup zero", func(c *config.Config) { c.Collectors.Flowlogs.DedupCapacity = 0 }, "collectors.flowlogs.dedup_capacity"},
		{"audit dedup negative", func(c *config.Config) { c.Collectors.Auditlogs.DedupCapacity = -1 }, "collectors.auditlogs.dedup_capacity"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			tc.set(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestValidatePerTailnetCardinalityUsesEffectiveValues(t *testing.T) {
	cfg := config.Default()
	cfg.Tailnets = []config.TailnetConfig{{Name: "one", Auth: cfg.Tailscale.Auth, Cardinality: config.TailnetCardinality{MetricLimit: 1000}}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "exceed effective metric_limit 1000") {
		t.Fatalf("Validate inherited thresholds = %v, want effective-limit error", err)
	}

	cfg = config.Default()
	cfg.Tailnets = []config.TailnetConfig{{
		Name: "one",
		Auth: cfg.Tailscale.Auth,
		Cardinality: config.TailnetCardinality{
			MetricLimit:       -1,
			WarningThreshold:  100,
			CriticalThreshold: 200,
		},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate unlimited per-tailnet limit = %v, want nil", err)
	}
}

// TestResolvedTailnets_CardinalityValuesAreEffective pins the three-way
// per-tailnet limit semantics at the config/app boundary: zero inherits, a
// negative limit is an explicit unlimited override, and thresholds inherit
// independently.
func TestResolvedTailnets_CardinalityValuesAreEffective(t *testing.T) {
	cfg := config.Default()
	cfg.Cardinality.MetricLimit = 100
	cfg.Cardinality.WarningThreshold = 40
	cfg.Cardinality.CriticalThreshold = 80
	cfg.Tailnets = []config.TailnetConfig{
		{Name: "inherited", Auth: cfg.Tailscale.Auth},
		{Name: "unlimited", Auth: cfg.Tailscale.Auth, Cardinality: config.TailnetCardinality{
			MetricLimit:       -1,
			WarningThreshold:  20,
			CriticalThreshold: 60,
		}},
		{Name: "mixed", Auth: cfg.Tailscale.Auth, Cardinality: config.TailnetCardinality{
			MetricLimit:       50,
			CriticalThreshold: 45,
		}},
	}

	got := cfg.ResolvedTailnets()
	if len(got) != 3 {
		t.Fatalf("ResolvedTailnets len = %d, want 3", len(got))
	}
	want := []struct{ limit, warning, critical int }{
		{100, 40, 80},
		{-1, 20, 60},
		{50, 40, 45},
	}
	for i, w := range want {
		if got[i].CardinalityMetricLimit != w.limit || got[i].CardinalityWarningThreshold != w.warning || got[i].CardinalityCriticalThreshold != w.critical {
			t.Errorf("ResolvedTailnets()[%d] cardinality = %d/%d/%d, want %d/%d/%d",
				i, got[i].CardinalityMetricLimit, got[i].CardinalityWarningThreshold, got[i].CardinalityCriticalThreshold,
				w.limit, w.warning, w.critical)
		}
	}
}

func validHeadscaleConfig() *config.Config {
	cfg := config.Default()
	cfg.Provider = "headscale"
	cfg.Headscale.URL = "https://headscale.example.test"
	cfg.Headscale.APIKey = "test-key"
	return cfg
}
