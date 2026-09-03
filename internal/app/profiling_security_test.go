package app

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grafana/pyroscope-go"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestStartProfilingRefusesTLSFallback(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "https://profiles.example.com"
	cfg.Profiling.Pyroscope.TLS.CAFile = filepath.Join(t.TempDir(), "rotated-away-ca.pem")

	profiler, err := startProfiling(cfg, "v-test", slog.New(slog.DiscardHandler))
	if err == nil {
		if profiler != nil {
			_ = profiler.Stop()
		}
		t.Fatal("startProfiling accepted unusable explicit TLS material")
	}
	if profiler != nil {
		_ = profiler.Stop()
		t.Fatal("profiler started despite unusable explicit TLS material")
	}
	if !strings.Contains(err.Error(), "TLS") && !strings.Contains(err.Error(), "tls") {
		t.Fatalf("error = %v, want TLS construction failure", err)
	}
}

func mustPyroscopeConfigWithUploadClient(t *testing.T, cfg *config.Config, version string, opts ...profilingOption) pyroscope.Config {
	t.Helper()
	pc, err := pyroscopeConfigWithUploadClient(cfg, version, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return pc
}
