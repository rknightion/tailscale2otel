package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// ran is a small helper mirroring health_test.go's synthetic collector-status
// builder, so readinessVerdict can be exercised without a live App/scheduler.
func readyRan(name string, ok bool) statusdata.CollectorStatus {
	return statusdata.CollectorStatus{Name: name, HasRun: true, LastSuccess: ok}
}

func TestReadinessVerdict(t *testing.T) {
	t.Run("all collectors ran, no receiver failures -> ready", func(t *testing.T) {
		ready, reason := readinessVerdict([]statusdata.CollectorStatus{readyRan("devices", true)}, nil)
		if !ready || reason != "" {
			t.Fatalf("got ready=%v reason=%q, want ready with no reason", ready, reason)
		}
	})

	t.Run("no collectors registered -> ready", func(t *testing.T) {
		ready, reason := readinessVerdict(nil, nil)
		if !ready || reason != "" {
			t.Fatalf("got ready=%v reason=%q, want ready with no reason", ready, reason)
		}
	})

	t.Run("a collector pending its first run -> not ready, reason mentions starting", func(t *testing.T) {
		ready, reason := readinessVerdict([]statusdata.CollectorStatus{
			readyRan("devices", true),
			{Name: "flowlogs", HasRun: false},
		}, nil)
		if ready {
			t.Fatal("got ready=true, want not-ready while a collector is still starting")
		}
		if !strings.Contains(reason, "starting") || !strings.Contains(reason, "flowlogs") {
			t.Fatalf("reason = %q, want it to mention starting + flowlogs", reason)
		}
	})

	t.Run("a merely degraded collector does NOT gate readiness", func(t *testing.T) {
		// 3+ consecutive failures makes deriveHealth report "degraded", not
		// "starting" — that alone must not flip /readyz, per the issue's
		// acceptance criteria (only "starting" and receiver failure gate it).
		degraded := statusdata.CollectorStatus{Name: "keys", HasRun: true, LastSuccess: false, ConsecutiveFailures: 5}
		ready, reason := readinessVerdict([]statusdata.CollectorStatus{degraded}, nil)
		if !ready || reason != "" {
			t.Fatalf("got ready=%v reason=%q, want ready (degraded collectors don't gate readiness)", ready, reason)
		}
	})

	// The tracker stopped being receiver-specific when the admin and Prometheus
	// listeners started feeding it (#306), so the reason says "component".
	t.Run("component failure with no pending collectors -> not ready, reason mentions the failure", func(t *testing.T) {
		ready, reason := readinessVerdict(
			[]statusdata.CollectorStatus{readyRan("devices", true)},
			[]string{"stream: listen tcp :8088: bind: address already in use"},
		)
		if ready {
			t.Fatal("got ready=true, want not-ready when a receiver has terminally failed")
		}
		if !strings.Contains(reason, "component failure") || !strings.Contains(reason, "8088") {
			t.Fatalf("reason = %q, want it to mention the component failure + the underlying error", reason)
		}
	})

	t.Run("starting takes precedence over a receiver failure in the reported reason", func(t *testing.T) {
		ready, reason := readinessVerdict(
			[]statusdata.CollectorStatus{{Name: "devices", HasRun: false}},
			[]string{"webhook: bind failed"},
		)
		if ready {
			t.Fatal("got ready=true, want not-ready")
		}
		if !strings.Contains(reason, "starting") {
			t.Fatalf("reason = %q, want the starting reason to take precedence", reason)
		}
	})
}

// TestReadyzHandler_ServesVerdict is a thin HTTP-level check that (*App).readyz
// wires readinessVerdict's result onto the response correctly (status code,
// Content-Type, and body) — the decision logic itself is covered exhaustively
// above and in the buildAdminServer-level tests in admin_status_test.go.
func TestReadyzHandler_ServesVerdict(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	a.readyz(w, req)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz on a fresh app = %d, want 503", w.Code)
	}
}

func TestReadyzHandler_GatesOnIngressWALLifecycle(t *testing.T) {
	for _, state := range []ingressWALState{
		ingressWALStateRetrying,
		ingressWALStateFull,
		ingressWALStateFailed,
		ingressWALStateDraining,
		ingressWALStateStopped,
	} {
		t.Run(string(state), func(t *testing.T) {
			a := &App{
				readyState: newComponentHealth(),
				ingressWAL: &ingressWALCoordinator{state: state},
			}
			w := httptest.NewRecorder()
			a.readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("state %q status = %d, want 503", state, w.Code)
			}
			// The component is named the same way everywhere now — appcatalog's
			// value, not a second human spelling (#318).
			if !strings.Contains(w.Body.String(), appcatalog.ComponentIngressWAL) ||
				!strings.Contains(w.Body.String(), string(state)) {
				t.Fatalf("state %q body = %q, want bounded WAL state", state, w.Body.String())
			}
		})
	}

	// replaying belongs HERE, not above: the live worker sets it on every drain
	// cycle, so gating readiness on it made /readyz flap 503 against the worker's
	// duty cycle and, on a leader promoted onto a large inherited backlog, held
	// 503 for the whole drain — pulling the leader-labeled pod out of the
	// receiver and admin Services. The startup drain gates readiness through
	// walStartupPending instead, which is bounded; see the test below.
	for _, state := range []ingressWALState{
		ingressWALStateDisabled,
		ingressWALStateReady,
		ingressWALStateReplaying,
	} {
		t.Run(string(state), func(t *testing.T) {
			a := &App{
				readyState: newComponentHealth(),
				ingressWAL: &ingressWALCoordinator{state: state},
			}
			w := httptest.NewRecorder()
			a.readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("state %q status = %d, want 200: %q", state, w.Code, w.Body.String())
			}
		})
	}
}

func TestReadyzHandler_IngressWALFailurePrecedesCollectorsStarting(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	a.ingressWAL = &ingressWALCoordinator{state: ingressWALStateFailed}

	w := httptest.NewRecorder()
	a.readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got := w.Body.String(); got != appcatalog.ComponentIngressWAL+": failed" {
		t.Fatalf("body = %q, want WAL failure to precede collector startup", got)
	}
}

// TestReadyzHandler_StartupDrainGatesReadinessThenClears pins the replacement
// for the removed "replaying is a failure" gate. A startup drain still holds
// /readyz at 503, because the stream and webhook listeners are not open yet and
// a leader-labeled pod must not be routed traffic it would refuse — but it says
// so as a drain, reports how much is left, and clears on its own.
func TestReadyzHandler_StartupDrainGatesReadinessThenClears(t *testing.T) {
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{{ID: "a", Body: []byte("xy")}}}
	a := &App{
		readyState: newComponentHealth(),
		ingressWAL: &ingressWALCoordinator{wal: wal, state: ingressWALStateReplaying},
	}
	a.walStartupPending.Store(true)

	w := httptest.NewRecorder()
	a.readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status during startup drain = %d, want 503", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, appcatalog.ComponentIngressWAL) ||
		!strings.Contains(body, "draining startup backlog") ||
		!strings.Contains(body, "1 entries") {
		t.Fatalf("body = %q, want the component, the drain, and the remaining backlog", body)
	}

	a.walStartupPending.Store(false)
	w = httptest.NewRecorder()
	a.readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status after the drain released receivers = %d, want 200: %q", w.Code, w.Body.String())
	}
}

// TestIngressWALReplayingIsNotAFailedComponent guards the status page against
// the probe's old verdict: an operator looking at a promoted leader mid-drain
// saw ingress_wal marked Failed for work that was proceeding normally.
func TestIngressWALReplayingIsNotAFailedComponent(t *testing.T) {
	a := &App{
		readyState: newComponentHealth(),
		cfg:        config.Default(),
		ingressWAL: &ingressWALCoordinator{state: ingressWALStateReplaying},
	}
	if reasons := a.componentFailureReasons(); len(reasons) != 0 {
		t.Fatalf("componentFailureReasons() = %v, want none while replaying", reasons)
	}
	for _, row := range a.componentStatuses(a.componentFailureReasons()) {
		if row.Name == appcatalog.ComponentIngressWAL && row.Failed {
			t.Fatalf("status page marked %s Failed while replaying (reason %q)", row.Name, row.Reason)
		}
	}
	// A genuine failure must still be reported, or this test would pass on a
	// gate that reports nothing at all.
	a.ingressWAL = &ingressWALCoordinator{state: ingressWALStateFailed}
	if reasons := a.componentFailureReasons(); len(reasons) != 1 {
		t.Fatalf("componentFailureReasons() = %v, want the failed state reported", reasons)
	}
}
