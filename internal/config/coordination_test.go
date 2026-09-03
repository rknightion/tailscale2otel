package config_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestCoordinationDefaultsAndValidation(t *testing.T) {
	c := config.Default()
	if c.Coordination.Mode != "none" {
		t.Fatalf("Coordination.Mode = %q, want none", c.Coordination.Mode)
	}
	if c.Coordination.LeaseName != "tailscale2otel" || c.Coordination.Namespace != "default" {
		t.Fatalf("unexpected lease defaults: %#v", c.Coordination)
	}
	if c.Coordination.LeaseDuration.D() <= c.Coordination.RenewDeadline.D() ||
		c.Coordination.RenewDeadline.D() <= c.Coordination.RetryPeriod.D() {
		t.Fatalf("invalid coordination timing defaults: %#v", c.Coordination)
	}

	for _, yaml := range []string{
		"coordination:\n  mode: unsupported\n",
		"checkpoint:\n  store: kubernetes\n",
		"coordination:\n  mode: kubernetes\n  lease_name: Bad_Name\n",
		"coordination:\n  mode: kubernetes\n  lease_duration: 10s\n  renew_deadline: 10s\n  retry_period: 2s\n",
	} {
		if err := loadErr(t, yaml); err == nil {
			t.Fatalf("Load(%q) succeeded, want coordination validation failure", yaml)
		}
	}

	if err := loadErr(t, "coordination:\n  mode: kubernetes\ncheckpoint:\n  store: kubernetes\n"); err != nil {
		t.Fatalf("checkpoint.store=kubernetes rejected: %v", err)
	}
}

func TestCoordinationBothSourceWarning(t *testing.T) {
	c := config.Default()
	c.Coordination.Mode = "kubernetes"
	c.Collectors.Flowlogs.Source = "both"
	if warnings := strings.Join(c.Warnings(), "\n"); !strings.Contains(warnings, "cross-source de-duplication") {
		t.Fatalf("warnings = %q, want source=both coordination warning", warnings)
	}
}
