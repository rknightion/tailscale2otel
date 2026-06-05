package app

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// stubWindowCollector is a minimal WindowCollector for exercising checkpoint
// state on the status page without polling a real Tailscale endpoint.
type stubWindowCollector struct {
	name string
	lag  time.Duration
}

func (s stubWindowCollector) Name() string                   { return s.name }
func (s stubWindowCollector) DefaultInterval() time.Duration { return time.Minute }
func (s stubWindowCollector) Lag() time.Duration             { return s.lag }
func (s stubWindowCollector) CollectWindow(context.Context, time.Time, time.Time, telemetry.Emitter) (time.Time, error) {
	return time.Time{}, nil
}

func TestBuildStatus_WindowCheckpointStuck(t *testing.T) {
	store := collector.NewMemoryStore()
	if err := store.Set("flowlogs", time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	a := newApp(config.Default(), "vtest", nil, telemetrytest.New().Emitter(),
		func(context.Context) error { return nil }, newTestClient(t, "http://127.0.0.1:0"),
		store, NewAPIStats())
	a.registry.RegisterWindow(stubWindowCollector{name: "flowlogs", lag: time.Minute}, time.Minute, 0, 0)

	st := a.buildStatus()
	var cp *statusdata.CheckpointStatus
	for _, c := range st.Collectors {
		if c.Name == "flowlogs" {
			cp = c.Checkpoint
		}
	}
	if cp == nil {
		t.Fatal("flowlogs has no checkpoint state")
	}
	if !cp.Stuck {
		t.Errorf("checkpoint should be stuck (2h lag >> 1m interval)")
	}
	if cp.LagSec < 7000 {
		t.Errorf("lag = %ds, want ~7200", cp.LagSec)
	}
}

func TestBuildStatus_HasAPISection(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	st := a.buildStatus()
	if st.API.Endpoints == nil {
		t.Errorf("API.Endpoints should be a non-nil (possibly empty) slice")
	}
}
