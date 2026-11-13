package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRejectsUnitlessDuration(t *testing.T) {
	for _, value := range []string{"30", "30.5", "-30", "\"30\""} {
		t.Run(value, func(t *testing.T) {
			_, err := Load(writeTempConfig(t, "tailscale:\n  http:\n    timeout: "+value+"\n"))
			if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "unit") {
				t.Fatalf("unitless timeout should name the key and missing unit: %v", err)
			}
		})
	}
	for _, value := range []string{"0", "0.0", "\"0s\""} {
		t.Run("zero_"+value, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, "delivery:\n  log_max_record_age: "+value+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Delivery.LogMaxRecordAge != 0 {
				t.Fatalf("age = %v", cfg.Delivery.LogMaxRecordAge)
			}
		})
	}
	t.Run("defaults", func(t *testing.T) {
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tailscale.HTTP.Timeout != Default().Tailscale.HTTP.Timeout {
			t.Fatal("default timeout changed")
		}
	})
	t.Run("environment", func(t *testing.T) {
		t.Setenv("TS2OTEL_TAILSCALE__HTTP__TIMEOUT", "45s")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tailscale.HTTP.Timeout.D() != 45*time.Second {
			t.Fatalf("timeout = %v", cfg.Tailscale.HTTP.Timeout)
		}
	})
}
