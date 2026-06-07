package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// validCfg returns a *config.Config that passes Validate() and has zero Warnings().
func validCfg() *config.Config {
	return config.Default()
}

// cfgWithWarning returns a *config.Config that passes Validate() but yields
// exactly one advisory from Warnings() — the apikey auth-method advisory.
func cfgWithWarning() *config.Config {
	c := config.Default()
	c.Tailscale.Auth.Method = "apikey"
	return c
}

// cfgInvalid returns a *config.Config whose Validate() returns a non-nil error.
// We use an invalid otlp.protocol value, which is the simplest hard error.
func cfgInvalid() *config.Config {
	c := config.Default()
	c.OTLP.Protocol = "invalid-proto"
	return c
}

func TestEmitConfigHealth_ValidConfig_NoWarnings(t *testing.T) {
	rec := telemetrytest.New()
	cfg := validCfg()

	emitConfigHealth(rec.Emitter(), cfg)

	warnPts := rec.MetricPoints("tailscale2otel.config.warnings")
	if len(warnPts) != 1 {
		t.Fatalf("config.warnings: got %d points, want 1", len(warnPts))
	}
	if warnPts[0].Kind != "gauge" {
		t.Errorf("config.warnings: kind = %q, want gauge", warnPts[0].Kind)
	}
	want := float64(len(cfg.Warnings()))
	if warnPts[0].Value != want {
		t.Errorf("config.warnings: value = %v, want %v", warnPts[0].Value, want)
	}

	validPts := rec.MetricPoints("tailscale2otel.config.valid")
	if len(validPts) != 1 {
		t.Fatalf("config.valid: got %d points, want 1", len(validPts))
	}
	if validPts[0].Kind != "gauge" {
		t.Errorf("config.valid: kind = %q, want gauge", validPts[0].Kind)
	}
	if validPts[0].Value != 1 {
		t.Errorf("config.valid: value = %v, want 1", validPts[0].Value)
	}
}

func TestEmitConfigHealth_ConfigWithWarning(t *testing.T) {
	rec := telemetrytest.New()
	cfg := cfgWithWarning()

	emitConfigHealth(rec.Emitter(), cfg)

	warnPts := rec.MetricPoints("tailscale2otel.config.warnings")
	if len(warnPts) != 1 {
		t.Fatalf("config.warnings: got %d points, want 1", len(warnPts))
	}
	want := float64(len(cfg.Warnings()))
	if want == 0 {
		t.Fatal("cfgWithWarning() returned zero warnings — test setup broken")
	}
	if warnPts[0].Value != want {
		t.Errorf("config.warnings: value = %v, want %v", warnPts[0].Value, want)
	}

	// apikey method is still valid per Validate(), so config.valid must be 1.
	validPts := rec.MetricPoints("tailscale2otel.config.valid")
	if len(validPts) != 1 {
		t.Fatalf("config.valid: got %d points, want 1", len(validPts))
	}
	if validPts[0].Value != 1 {
		t.Errorf("config.valid: value = %v, want 1 (apikey is warn, not invalid)", validPts[0].Value)
	}
}

func TestEmitConfigHealth_InvalidConfig(t *testing.T) {
	rec := telemetrytest.New()
	cfg := cfgInvalid()

	if cfg.Validate() == nil {
		t.Fatal("cfgInvalid() unexpectedly passed Validate() — test setup broken")
	}

	emitConfigHealth(rec.Emitter(), cfg)

	validPts := rec.MetricPoints("tailscale2otel.config.valid")
	if len(validPts) != 1 {
		t.Fatalf("config.valid: got %d points, want 1", len(validPts))
	}
	if validPts[0].Value != 0 {
		t.Errorf("config.valid: value = %v, want 0 for invalid config", validPts[0].Value)
	}
}

func TestRunConfigHealthReporter_EmitsImmediatelyThenOnTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		cfg := validCfg()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go runConfigHealthReporter(ctx, cfg, rec.Emitter(), time.Hour)

		// Initial emit should have happened before the goroutine blocks in select.
		synctest.Wait()

		warnPts := rec.MetricPoints("tailscale2otel.config.warnings")
		if len(warnPts) != 1 {
			t.Fatalf("after start: config.warnings = %+v, want 1 point", warnPts)
		}
		validPts := rec.MetricPoints("tailscale2otel.config.valid")
		if len(validPts) != 1 || validPts[0].Value != 1 {
			t.Fatalf("after start: config.valid = %+v, want value 1", validPts)
		}

		// Advance fake clock by one interval; ticker fires and re-emits.
		time.Sleep(time.Hour)
		synctest.Wait()

		warnPts = rec.MetricPoints("tailscale2otel.config.warnings")
		if len(warnPts) != 1 {
			t.Fatalf("after tick: config.warnings = %+v, want 1 point", warnPts)
		}
		validPts = rec.MetricPoints("tailscale2otel.config.valid")
		if len(validPts) != 1 || validPts[0].Value != 1 {
			t.Fatalf("after tick: config.valid = %+v, want value 1", validPts)
		}
	})
}

func TestRunConfigHealthReporter_DefaultInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		cfg := validCfg()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// interval <= 0 should default to 60s.
		go runConfigHealthReporter(ctx, cfg, rec.Emitter(), 0)
		synctest.Wait()

		// Verify initial emit happened.
		warnPts := rec.MetricPoints("tailscale2otel.config.warnings")
		if len(warnPts) != 1 {
			t.Fatalf("default interval: config.warnings = %+v, want 1 point", warnPts)
		}

		// Advance 60s (the default interval) — ticker should fire.
		time.Sleep(60 * time.Second)
		synctest.Wait()

		warnPts = rec.MetricPoints("tailscale2otel.config.warnings")
		if len(warnPts) != 1 {
			t.Fatalf("after 60s tick: config.warnings = %+v, want 1 point", warnPts)
		}
	})
}
