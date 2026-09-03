package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
