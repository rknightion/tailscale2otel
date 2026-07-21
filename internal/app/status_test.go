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

// TestBuildStatus_ThroughputAndFleetTrend asserts the sampler's throughput and
// collector-fleet rings reach the status snapshot: the current rate is the most
// recent sample (not a cumulative total) and each trend series is carried
// through for the charts.
func TestBuildStatus_ThroughputAndFleetTrend(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	t0 := time.Now()
	a.runtimeHist.sample(t0, samplerTick{
		emit:  telemetry.EmitStats{MetricPoints: 100, LogRecords: 10},
		fleet: fleetStats{active: 2, failing: 1, meanDurationMs: 40},
	})
	a.runtimeHist.sample(t0.Add(10*time.Second), samplerTick{
		emit:  telemetry.EmitStats{MetricPoints: 200, LogRecords: 30},
		fleet: fleetStats{active: 2, failing: 0, meanDurationMs: 60},
	})

	st := a.buildStatus()
	if got := st.Throughput.MetricPointsPerSec; got != 10 {
		t.Errorf("Throughput.MetricPointsPerSec = %v, want 10", got)
	}
	if got := st.Throughput.LogRecordsPerSec; got != 2 {
		t.Errorf("Throughput.LogRecordsPerSec = %v, want 2", got)
	}
	if got := st.Throughput.MetricPointsPerSecSeries; len(got) != 2 || got[1] != 10 {
		t.Errorf("Throughput.MetricPointsPerSecSeries = %v, want 2 samples ending in 10", got)
	}
	if got := st.Throughput.LogRecordsPerSecSeries; len(got) != 2 || got[1] != 2 {
		t.Errorf("Throughput.LogRecordsPerSecSeries = %v, want 2 samples ending in 2", got)
	}
	if got := st.Fleet.FailingSeries; len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Errorf("Fleet.FailingSeries = %v, want [1 0]", got)
	}
	if got := st.Fleet.MeanDurationMsSeries; len(got) != 2 || got[1] != 60 {
		t.Errorf("Fleet.MeanDurationMsSeries = %v, want 2 samples ending in 60", got)
	}
}

// TestBuildStatus_FleetIsLiveNotSampled asserts the headline fleet numbers are
// read from the collector status tracker at build time rather than replayed from
// the sampler ring — a collector that has never run leaves them at zero even
// after the ring has recorded non-zero samples. (The reduction itself is covered
// exhaustively by TestFleetAggregate.)
func TestBuildStatus_FleetIsLiveNotSampled(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	a.runtimeHist.sample(time.Now(), samplerTick{fleet: fleetStats{active: 9, failing: 4, meanDurationMs: 55}})

	st := a.buildStatus()
	if st.Fleet.Active != 0 || st.Fleet.Failing != 0 || st.Fleet.MeanDurationMs != 0 {
		t.Errorf("Fleet = %+v, want zero (no collector has run yet)", st.Fleet)
	}
	if got := st.Fleet.FailingSeries; len(got) != 1 || got[0] != 4 {
		t.Errorf("Fleet.FailingSeries = %v, want [4] (the sampled trend is still carried)", got)
	}
}
