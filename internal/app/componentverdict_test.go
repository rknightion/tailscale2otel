package app

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

// /readyz and the status page answered the same question from different state:
// readiness consulted component failures, the page's health verdict looked only
// at collectors. So a failed receiver or a listener that never bound produced
// 503 from the probe and "healthy" from the page an operator was staring at
// (#318).

func verdictApp(t *testing.T) *App {
	t.Helper()
	return &App{
		cfg:        config.Default(),
		logger:     slog.New(slog.DiscardHandler),
		readyState: newComponentHealth(),
	}
}

// ran is a collector that has completed a successful first tick, i.e. one that
// contributes nothing to the health verdict on its own.
func ranOK(name string) statusdata.CollectorStatus {
	return statusdata.CollectorStatus{Name: name, HasRun: true, LastSuccess: true}
}

func TestComponentFailureDegradesTheStatusVerdict(t *testing.T) {
	collectors := []statusdata.CollectorStatus{ranOK("devices")}
	if health, _ := deriveHealth(collectors, nil); health != healthHealthy {
		t.Fatalf("baseline health = %q, want %q: the fixture must be healthy before the "+
			"failure, or the test proves nothing", health, healthHealthy)
	}

	health, reasons := deriveHealth(collectors, []string{"admin: listen tcp :9091: bind: address already in use"})
	if health == healthHealthy {
		t.Fatalf("health = %q with a failed component. The page said healthy while /readyz "+
			"returned 503 for the same state.", health)
	}
	if health != healthDegraded {
		t.Errorf("health = %q, want %q", health, healthDegraded)
	}
	joined := strings.Join(reasons, "; ")
	if !strings.Contains(joined, appcatalog.ComponentAdmin) {
		t.Errorf("health reasons = %v, want one naming the failed component", reasons)
	}
	if !strings.Contains(joined, "9091") {
		t.Errorf("health reasons = %v, want the underlying error, or an operator has to go "+
			"read the logs anyway", reasons)
	}
}

// The two surfaces must not merely both be unhappy — they must be unhappy for
// the same inputs, which is the only way a page and a probe stop disagreeing.
func TestReadinessAndStatusAgreeOnIdenticalState(t *testing.T) {
	cases := []struct {
		name       string
		collectors []statusdata.CollectorStatus
		failures   []string
		wantReady  bool
		wantHealth string
	}{
		{"all good", []statusdata.CollectorStatus{ranOK("devices")}, nil, true, healthHealthy},
		{"still starting", []statusdata.CollectorStatus{{Name: "devices"}}, nil, false, healthStarting},
		{"listener failed", []statusdata.CollectorStatus{ranOK("devices")}, []string{"metrics: bind: address already in use"}, false, healthDegraded},
		{"receiver failed", []statusdata.CollectorStatus{ranOK("devices")}, []string{"stream: unexpected stop"}, false, healthDegraded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ready, readyReason := readinessVerdict(tc.collectors, tc.failures)
			health, _ := deriveHealth(tc.collectors, tc.failures)
			if ready != tc.wantReady {
				t.Errorf("ready = %v (%q), want %v", ready, readyReason, tc.wantReady)
			}
			if health != tc.wantHealth {
				t.Errorf("health = %q, want %q", health, tc.wantHealth)
			}
			// The contract that matters: never ready-but-unhealthy or
			// unready-but-healthy for one set of inputs.
			if ready && health != healthHealthy {
				t.Errorf("/readyz says ready while the page says %q for identical state", health)
			}
			if !ready && health == healthHealthy {
				t.Errorf("/readyz says unready (%q) while the page says healthy for identical state", readyReason)
			}
		})
	}
}

// A degraded collector is deliberately NOT a readiness gate (#57), so the two
// surfaces are allowed to differ there — and that exception has to stay
// deliberate rather than become an accident of the shared function.
func TestDegradedCollectorsStillDoNotGateReadiness(t *testing.T) {
	collectors := []statusdata.CollectorStatus{{Name: "devices", HasRun: true, LastSuccess: false}}
	ready, _ := readinessVerdict(collectors, nil)
	health, _ := deriveHealth(collectors, nil)
	if !ready {
		t.Error("a collector whose last run failed must not pull the pod out of rotation")
	}
	if health != healthDegraded {
		t.Errorf("health = %q, want %q: the page still shows it", health, healthDegraded)
	}
}

func TestComponentStatusesReportEnabledAndFailedState(t *testing.T) {
	a := verdictApp(t)
	a.cfg.Prometheus.Enabled = true
	a.cfg.Streaming.Enabled = false
	a.readyState.fail(appcatalog.ComponentMetrics, errors.New("bind: address already in use"))

	byName := map[string]statusdata.ComponentStatus{}
	for _, c := range a.componentStatuses(a.componentFailureReasons()) {
		byName[c.Name] = c
	}

	metrics, ok := byName[appcatalog.ComponentMetrics]
	if !ok {
		t.Fatalf("no %q row in %v", appcatalog.ComponentMetrics, byName)
	}
	if !metrics.Enabled || !metrics.Failed {
		t.Errorf("metrics row = %+v, want enabled and failed", metrics)
	}
	if !strings.Contains(metrics.Reason, "address already in use") {
		t.Errorf("metrics reason = %q, want the underlying error", metrics.Reason)
	}

	stream, ok := byName[appcatalog.ComponentStream]
	if !ok {
		t.Fatalf("no %q row: a disabled component is still worth showing, or an operator "+
			"cannot tell 'off' from 'missing'", appcatalog.ComponentStream)
	}
	if stream.Enabled || stream.Failed {
		t.Errorf("stream row = %+v, want neither enabled nor failed", stream)
	}
}

// Liveness is process-only: a component failure means "stop sending me traffic",
// not "restart me". Conflating them turns a bad config into a crash loop.
func TestHealthzStaysProcessOnly(t *testing.T) {
	a := verdictApp(t)
	a.readyState.fail(appcatalog.ComponentAdmin, errors.New("bind: address already in use"))

	mux := http.NewServeMux()
	registerProbes(mux, a.readyz)

	live := httptest.NewRecorder()
	mux.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if live.Code != http.StatusOK {
		t.Errorf("/healthz = %d after a component failure, want 200: liveness is process-only "+
			"and a 503 here would restart a process that is running fine", live.Code)
	}

	ready := httptest.NewRecorder()
	mux.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d after a component failure, want 503", ready.Code)
	}
}
