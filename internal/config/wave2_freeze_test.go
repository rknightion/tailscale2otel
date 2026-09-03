package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestWave2FreezeDefaultsPreserveCurrentBehavior(t *testing.T) {
	cfg := config.Default()
	if cfg.Collectors.Acl.SnapshotEnabled ||
		cfg.Collectors.Settings.SnapshotEnabled ||
		cfg.Collectors.Dns.SnapshotEnabled ||
		cfg.Collectors.Webhooks.SnapshotEnabled ||
		cfg.Collectors.PostureIntegrations.SnapshotEnabled ||
		cfg.Collectors.Devices.ChangeLogEnabled {
		t.Fatal("a Wave 2 snapshot/change-log feature defaults on")
	}
	if got := cfg.Collectors.Acl.SnapshotHeartbeat.D(); got != 24*time.Hour {
		t.Fatalf("ACL snapshot heartbeat = %v, want 24h", got)
	}

	wantCategories := map[string]struct {
		gotEnabled bool
		gotRollup  bool
		wantRollup bool
	}{
		"policy_change": {cfg.GrafanaAnnotations.Categories.PolicyChange.Enabled, cfg.GrafanaAnnotations.Categories.PolicyChange.Rollup, false},
		"inventory":     {cfg.GrafanaAnnotations.Categories.Inventory.Enabled, cfg.GrafanaAnnotations.Categories.Inventory.Rollup, true},
		"risk":          {cfg.GrafanaAnnotations.Categories.Risk.Enabled, cfg.GrafanaAnnotations.Categories.Risk.Rollup, false},
	}
	for name, category := range wantCategories {
		if !category.gotEnabled || category.gotRollup != category.wantRollup {
			t.Errorf("category %s = enabled:%v rollup:%v, want enabled:true rollup:%v", name, category.gotEnabled, category.gotRollup, category.wantRollup)
		}
	}
}

func TestWave2FreezeRejectsInvalidSnapshotHeartbeat(t *testing.T) {
	cfg := config.Default()
	cfg.Collectors.Acl.SnapshotHeartbeat = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "collectors.acl.snapshot_heartbeat") {
		t.Fatalf("Validate() error = %v, want collectors.acl.snapshot_heartbeat", err)
	}
}
