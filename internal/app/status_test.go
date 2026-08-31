package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/provider"
	"github.com/rknightion/tailscale2otel/v4/internal/redact"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
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

func TestBuildStatus_ConsolidatesDurableState(t *testing.T) {
	cfg := config.Default()
	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.EvidenceStore = "file"
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	a.checkpointEffective = "memory"
	a.checkpointReason = "checkpoint path is not writable"
	a.evidenceEffective = "file"
	a.evidencePath = "/state/checkpoints.json"

	got := a.buildStatus().DurableState
	if !got.Degraded {
		t.Fatal("DurableState.Degraded = false, want true when configured cursor durability fell back to memory")
	}
	if len(got.Stores) != 2 {
		t.Fatalf("DurableState.Stores = %d, want 2", len(got.Stores))
	}
	cursor, evidence := got.Stores[0], got.Stores[1]
	if cursor.ID != "poll_cursors" || cursor.Mode != "memory" || cursor.State != "degraded" || cursor.Reason == "" {
		t.Errorf("cursor durable state = %+v", cursor)
	}
	if evidence.ID != "semantic_evidence" || evidence.Mode != "file" || evidence.State != "durable" || evidence.Path == "" {
		t.Errorf("evidence durable state = %+v", evidence)
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

// --- credential redaction on the status surface (GHSA-qch3-gwff-r6pf,
// GHSA-jp5c-3282-6882, GHSA-h5p7-qj62-m8qx) ---------------------------------
//
// The admin status model is served to anyone who reaches the admin listener, so
// a URL configured with embedded credentials must be sanitized on the way into
// the DTO — not merely on the way out of one renderer.

const statusTestSecret = "sUpErSeCrEtCrEdEnTiAl"

func TestBuildStatus_RedactsOTLPEndpointCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.OTLP.Protocol = "http"
	cfg.OTLP.Endpoint = "https://123456:" + statusTestSecret + "@otlp.example.com/otlp?api_key=" + statusTestSecret + "#" + statusTestSecret
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	got := a.buildStatus().Telemetry.Endpoint
	if strings.Contains(got, statusTestSecret) {
		t.Fatalf("Telemetry.Endpoint = %q leaks the credential", got)
	}
	if want := redact.URLOrigin(cfg.OTLP.Endpoint); got != want {
		t.Errorf("Telemetry.Endpoint = %q, want %q", got, want)
	}
	if !strings.Contains(got, "otlp.example.com") {
		t.Errorf("Telemetry.Endpoint = %q: host should survive for diagnostics", got)
	}
}

func TestBuildStatus_RedactsPyroscopeServerAddress(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "https://user:" + statusTestSecret + "@profiles.example.com?token=" + statusTestSecret
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	got := a.buildStatus().Profiling.PyroscopeServer
	if strings.Contains(got, statusTestSecret) {
		t.Fatalf("Profiling.PyroscopeServer = %q leaks the credential", got)
	}
	if want := redact.URLOrigin(cfg.Profiling.Pyroscope.ServerAddress); got != want {
		t.Errorf("Profiling.PyroscopeServer = %q, want %q", got, want)
	}
}

func TestBuildStatus_RedactsNodeMetricsTargetURLs(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	raw := "https://scraper:" + statusTestSecret + "@node.example.com:5252/metrics?sig=" + statusTestSecret
	a.runtimes[0].nodeMetrics = nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: raw, Instance: "node-a"}},
	})

	nd := a.buildStatus().NodeDiscovery
	if len(nd.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(nd.Targets))
	}
	got := nd.Targets[0].URL
	if strings.Contains(got, statusTestSecret) {
		t.Fatalf("NodeDiscovery.Targets[0].URL = %q leaks the credential", got)
	}
	if want := redact.URLOrigin(raw); got != want {
		t.Errorf("NodeDiscovery.Targets[0].URL = %q, want %q", got, want)
	}
	if nd.Targets[0].Instance != "node-a" {
		t.Errorf("Instance = %q, want node-a (redaction must not disturb identity)", nd.Targets[0].Instance)
	}
}

func TestBuildStatus_DoesNotExposeObjectStoreCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Collectors.Flowlogs.ObjectStore.AccessKeyID = "S3ACCESS-status-canary"
	cfg.Collectors.Flowlogs.ObjectStore.SecretAccessKey = "S3SECRET-status-canary"
	cfg.Collectors.Flowlogs.ObjectStore.SessionToken = "S3SESSION-status-canary"
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	body, err := json.Marshal(a.buildStatus())
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	for _, secret := range []string{
		"S3ACCESS-status-canary",
		"S3SECRET-status-canary",
		"S3SESSION-status-canary",
	} {
		if strings.Contains(string(body), secret) {
			t.Errorf("status JSON leaked %q: %s", secret, body)
		}
	}
}

// TestBuildStatus_HasMetricsServingSection wires #377's status DTO fields
// (metrics.go's metricsScrapeHealth tracker) onto the real buildStatus() path,
// proving Status.MetricsServing is actually populated rather than only
// reachable via the package-internal a.metricsScrapeInfo() unit tests.
func TestBuildStatus_HasMetricsServingSection(t *testing.T) {
	cfg := config.Default()
	cfg.Prometheus.Enabled = true
	cfg.Prometheus.MaxRequestsInFlight = 5
	cfg.Prometheus.CoalesceGather = true
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	ms := a.buildStatus().MetricsServing
	if !ms.Enabled {
		t.Error("MetricsServing.Enabled = false, want true (prometheus.enabled)")
	}
	if ms.Config.MaxRequestsInFlight != 5 {
		t.Errorf("MetricsServing.Config.MaxRequestsInFlight = %d, want 5", ms.Config.MaxRequestsInFlight)
	}
	if !ms.Config.CoalesceGather {
		t.Error("MetricsServing.Config.CoalesceGather = false, want true")
	}
}
