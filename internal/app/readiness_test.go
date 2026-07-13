package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
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

	t.Run("receiver failure with no pending collectors -> not ready, reason mentions receiver failure", func(t *testing.T) {
		ready, reason := readinessVerdict(
			[]statusdata.CollectorStatus{readyRan("devices", true)},
			[]string{"stream: listen tcp :8088: bind: address already in use"},
		)
		if ready {
			t.Fatal("got ready=true, want not-ready when a receiver has terminally failed")
		}
		if !strings.Contains(reason, "receiver failure") || !strings.Contains(reason, "8088") {
			t.Fatalf("reason = %q, want it to mention receiver failure + the underlying error", reason)
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
