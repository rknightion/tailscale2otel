package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/coordination"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestCoordinationStandbyGatesReadinessAndEmitsLeaderState(t *testing.T) {
	cfg := config.Default()
	cfg.Coordination.Mode = "kubernetes"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)

	a.observeCoordination(coordination.Status{LeaseName: "tailscale2otel", Namespace: "default", Identity: "pod-a", State: coordination.StateStandby})
	w := httptest.NewRecorder()
	a.readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("standby /readyz = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "coordination: standby") {
		t.Fatalf("standby /readyz body = %q, want coordination standby reason", w.Body.String())
	}
	if got := a.buildStatus().Coordination; got.State != string(coordination.StateStandby) || got.Leader != "" {
		t.Fatalf("standby status = %#v, want standby with no leader", got)
	}

	a.observeCoordination(coordination.Status{LeaseName: "tailscale2otel", Namespace: "default", Identity: "pod-a", Leader: "pod-a", State: coordination.StateLeader})
	if got := a.buildStatus().Coordination; got.State != string(coordination.StateLeader) || got.Leader != "pod-a" {
		t.Fatalf("leader status = %#v, want leader pod-a", got)
	}
	points := rec.MetricPoints(appcatalog.MetricCoordinationLeader)
	values := make(map[string]float64, len(points))
	for _, point := range points {
		values[point.Attrs["coordination.state"]] = point.Value
	}
	if len(points) != 2 || values[string(coordination.StateStandby)] != 0 || values[string(coordination.StateLeader)] != 1 {
		t.Fatalf("leader metric points = %#v, want standby=0 and leader=1", points)
	}
	if _, ok := values[string(coordination.StateLeader)]; !ok {
		t.Fatalf("leader metric state labels = %#v, want leader", values)
	}
}

func TestCoordinationLeaseObservationEmitsOnlyCompletedHandover(t *testing.T) {
	cfg := config.Default()
	cfg.Coordination.Mode = "kubernetes"
	cfg.Coordination.LeaseName = "tailscale2otel"
	cfg.Coordination.Namespace = "default"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)

	initial := coordination.LeaseObservation{Initial: true, Identity: "pod-b"}
	a.observeCoordinationLease(initial)
	// A restarted observer also has an initial snapshot and must not look like
	// a handover. Renewal-only observations are ignored after the zero baseline.
	a.observeCoordinationLease(initial)
	a.observeCoordinationLease(coordination.LeaseObservation{Identity: "pod-b"})
	points := rec.MetricPoints(appcatalog.MetricCoordinationHandovers)
	if len(points) != 1 || points[0].Value != 0 || !points[0].Monotonic {
		t.Fatalf("pre-handover points = %#v, want one monotonic zero", points)
	}

	a.observeCoordinationLease(coordination.LeaseObservation{
		Identity:          "pod-b",
		CompletedHandover: true,
	})
	points = rec.MetricPoints(appcatalog.MetricCoordinationHandovers)
	if len(points) != 1 || points[0].Value != 1 {
		t.Fatalf("handover points = %#v, want one counter at 1", points)
	}
	for key, want := range map[string]string{
		"coordination.mode":       "kubernetes",
		"coordination.lease_name": "tailscale2otel",
		"coordination.namespace":  "default",
		"coordination.identity":   "pod-b",
	} {
		if got := points[0].Attrs[key]; got != want {
			t.Errorf("handover attr %q = %q, want %q", key, got, want)
		}
	}
}

// TestRunCoordinatedServesProcessMetricsWhileStandby pins the pull-path half
// of coordination: the standby keeps its process telemetry available, while
// collectors remain gated behind the Lease callback. The local host is not in
// Kubernetes, so coordinator construction is expected to fail after the
// listener has been started; that makes this a focused lifecycle test rather
// than a client-go integration test.
func TestRunCoordinatedServesProcessMetricsWhileStandby(t *testing.T) {
	cfg := config.Default()
	cfg.Coordination.Mode = "kubernetes"
	cfg.Prometheus.Enabled = true
	cfg.Prometheus.Listen = "127.0.0.1:2112"
	cfg.Prometheus.Auth.AllowUnauthenticated = true

	process := prometheus.NewRegistry()
	leader := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tailscale2otel_coordination_leader_ratio",
		Help: "lease leader state",
	}, []string{"coordination_mode", "coordination_state"})
	process.MustRegister(leader)
	leader.WithLabelValues("kubernetes", "standby").Set(0)

	metricsStarted := false
	a := &App{
		cfg:        cfg,
		logger:     slog.New(slog.DiscardHandler),
		readyState: newComponentHealth(),
		metricsRun: func(context.Context) { metricsStarted = true },
	}
	a.metricsSrv = a.buildMetricsServer(process)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.runCoordinated(ctx); err == nil {
		t.Fatal("runCoordinated unexpectedly succeeded without in-cluster Kubernetes configuration")
	}
	if !metricsStarted {
		t.Fatal("runCoordinated did not start the standby Prometheus listener")
	}
	rec := httptest.NewRecorder()
	a.metricsSrv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("standby /metrics status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `tailscale2otel_coordination_leader_ratio{coordination_mode="kubernetes",coordination_state="standby"} 0`) {
		t.Fatalf("standby /metrics lacks the coordination leader zero:\n%s", rec.Body.String())
	}
}

func TestCoordinationPromGathererHidesActiveSeriesOutsideLeadership(t *testing.T) {
	cfg := config.Default()
	cfg.Coordination.Mode = "kubernetes"
	process := prometheus.NewRegistry()
	coordinationMetric := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tailscale2otel_coordination_leader_ratio",
		Help: "lease leader state",
	})
	process.MustRegister(coordinationMetric)
	coordinationMetric.Set(0)
	active := prometheus.NewRegistry()
	collectorMetric := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tailscale2otel_devices_count_ratio",
		Help: "active collector output",
	})
	active.MustRegister(collectorMetric)
	collectorMetric.Set(7)

	a := &App{
		cfg:                 cfg,
		promGatherer:        prometheus.Gatherers{process, active},
		processPromGatherer: process,
	}
	gatherer := a.coordinationPromGatherer()
	hasMetric := func(name string) bool {
		t.Helper()
		families, err := gatherer.Gather()
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		for _, family := range families {
			if family.GetName() == name {
				return true
			}
		}
		return false
	}

	for _, state := range []coordination.State{coordination.StateStandby, coordination.StateSteppedDown} {
		a.coordinationStatus = coordination.Status{State: state}
		if !hasMetric("tailscale2otel_coordination_leader_ratio") {
			t.Errorf("%s gatherer omitted process coordination metric", state)
		}
		if hasMetric("tailscale2otel_devices_count_ratio") {
			t.Errorf("%s gatherer exposed stale active collector series", state)
		}
	}

	a.coordinationStatus = coordination.Status{State: coordination.StateLeader}
	if !hasMetric("tailscale2otel_devices_count_ratio") {
		t.Error("leader gatherer omitted active collector series")
	}
}
