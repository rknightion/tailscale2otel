package app

import (
	"slices"
	"testing"
	"time"

	"github.com/grafana/pyroscope-go"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

func TestPyroscopeConfig_Mapping(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "https://profiles-prod-1.grafana.net"
	cfg.Profiling.Pyroscope.BasicAuthUser = "12345"
	cfg.Profiling.Pyroscope.BasicAuthPassword = "secret-token"
	cfg.Profiling.Pyroscope.TenantID = "tenant-x"
	cfg.Profiling.Pyroscope.Tags = map[string]string{"env": "lab", "service_version": "ignored"}

	pc := pyroscopeConfig(cfg, "v9.9.9")

	if pc.ApplicationName != serviceName {
		t.Errorf("ApplicationName = %q, want %q", pc.ApplicationName, serviceName)
	}
	if pc.ServerAddress != "https://profiles-prod-1.grafana.net" {
		t.Errorf("ServerAddress = %q", pc.ServerAddress)
	}
	if pc.BasicAuthUser != "12345" || pc.BasicAuthPassword != "secret-token" {
		t.Errorf("basic auth = %q/%q", pc.BasicAuthUser, pc.BasicAuthPassword)
	}
	if pc.TenantID != "tenant-x" {
		t.Errorf("TenantID = %q", pc.TenantID)
	}
	if pc.Tags["service_version"] != "v9.9.9" {
		t.Errorf("service_version tag = %q, want v9.9.9 (must not be user-overridable)", pc.Tags["service_version"])
	}
	if pc.Tags["env"] != "lab" {
		t.Errorf("env tag = %q, want lab", pc.Tags["env"])
	}
}

func TestPyroscopeConfig_UploadRate(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.UploadRate = config.Duration(20 * time.Second)
	if got := pyroscopeConfig(cfg, "v1").UploadRate; got != 20*time.Second {
		t.Fatalf("UploadRate = %v, want 20s", got)
	}
}

func TestPyroscopeProfileTypes(t *testing.T) {
	// goroutine-leak is present only when built with GOEXPERIMENT=goroutineleakprofile,
	// so the expected count shifts by one between CI (plain) and release builds.
	leak := 0
	if goroutineLeakAvailable() {
		leak = 1
	}

	base := pyroscopeProfileTypes(config.ProfilingConfig{})
	if got, want := len(base), 6+leak; got != want {
		t.Fatalf("default profile types = %d, want %d (cpu+alloc/inuse+goroutines[+leak]): %v", got, want, base)
	}
	if slices.Contains(base, pyroscope.ProfileMutexCount) || slices.Contains(base, pyroscope.ProfileBlockCount) {
		t.Fatalf("profile types for an empty config must not include mutex/block: %v", base)
	}
	if goroutineLeakAvailable() && !slices.Contains(base, pyroscope.ProfileGoroutineLeak) {
		t.Errorf("goroutineleak available but not in profile set: %v", base)
	}

	withMutex := pyroscopeProfileTypes(config.ProfilingConfig{MutexProfileFraction: 5})
	if !slices.Contains(withMutex, pyroscope.ProfileMutexCount) || !slices.Contains(withMutex, pyroscope.ProfileMutexDuration) {
		t.Errorf("mutex fraction > 0 should add mutex profiles: %v", withMutex)
	}
	if slices.Contains(withMutex, pyroscope.ProfileBlockCount) {
		t.Errorf("mutex-only config must not add block profiles: %v", withMutex)
	}

	withBlock := pyroscopeProfileTypes(config.ProfilingConfig{BlockProfileRate: 5})
	if !slices.Contains(withBlock, pyroscope.ProfileBlockCount) || !slices.Contains(withBlock, pyroscope.ProfileBlockDuration) {
		t.Errorf("block rate > 0 should add block profiles: %v", withBlock)
	}
}
