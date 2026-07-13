package app

import (
	"context"
	"testing"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/provider"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
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
		tracenoop.NewTracerProvider().Tracer("test"),
		func(context.Context) error { return nil }, provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
		store, NewAPIStats())
	a.runtimes[0].registry.RegisterWindow(stubWindowCollector{name: "flowlogs", lag: time.Minute}, time.Minute, 0, 0)

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

// TestBuildStatus_CollectorInfo asserts each collector row carries the
// admin-tooltip data: a one-line purpose and the metrics it emits, sourced from
// the in-code catalog.
func TestBuildStatus_CollectorInfo(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Collectors.Devices.Enabled = true
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	st := a.buildStatus()
	var dev *statusdata.CollectorStatus
	for i := range st.Collectors {
		if st.Collectors[i].Name == "devices" {
			dev = &st.Collectors[i]
		}
	}
	if dev == nil {
		t.Fatal("devices collector missing from status")
	}
	if dev.Description == "" {
		t.Error("devices collector has no info description")
	}
	found := false
	for _, m := range dev.Metrics {
		if m.Name == "tailscale.device.online" {
			if m.Description == "" {
				t.Error("tailscale.device.online has empty description")
			}
			found = true
		}
	}
	if !found {
		t.Errorf("devices metrics missing tailscale.device.online; got %v", dev.Metrics)
	}
}
