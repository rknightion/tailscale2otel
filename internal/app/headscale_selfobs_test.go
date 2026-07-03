package app

import (
	"context"
	"log/slog"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/config"
)

// TestNew_HeadscaleRuntimeHasNoAliasedSelfObs pins #54: under provider=headscale
// the single runtime shares the PROCESS provider's emitter, so it must be built
// with nil card/exportStats. Otherwise the per-runtime self-obs reporters ran a
// second pair over the same tracker/stats as the process-level reporters,
// inflating export.* ~2x and corrupting series.active. Exercised through the real
// New() path (not the newApp nil-hook seam).
func TestNew_HeadscaleRuntimeHasNoAliasedSelfObs(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "headscale"
	cfg.Headscale.URL = "https://headscale.invalid"
	cfg.Headscale.APIKey = "hs-key"
	cfg.OTLP.Protocol = "stdout" // no network egress during the test
	cfg.SelfObservability.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cfg.Validate: %v", err)
	}

	a, err := New(context.Background(), cfg, "v-test", slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		t.Fatalf("New (headscale): %v", err)
	}
	t.Cleanup(func() {
		if a.restore != nil {
			a.restore()
		}
		if a.shutdown != nil {
			_ = a.shutdown(context.Background())
		}
	})

	if len(a.runtimes) != 1 {
		t.Fatalf("headscale runtimes = %d, want 1", len(a.runtimes))
	}
	rt := a.runtimes[0]
	if rt.card != nil {
		t.Error("headscale runtime.card must be nil (process provider covers its self-obs); a non-nil aliased tracker double-reports series.active")
	}
	if rt.exportStats != nil {
		t.Error("headscale runtime.exportStats must be nil; a non-nil aliased stats func double-reports export.datapoints/log_records")
	}
	// The process-level tracker still exists and is what the emitter feeds, so
	// self-obs is covered exactly once.
	if a.procCard == nil {
		t.Error("process cardinality tracker is nil with self_observability enabled")
	}
}

// discard is a no-op io.Writer for the test logger.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
