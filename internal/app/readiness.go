package app

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
)

// componentHealth tracks terminal failures of the long-running components so
// /readyz can report the service unready once one has failed to bind or stopped
// unexpectedly: the optional stream/webhook receivers (#57) and the admin and
// Prometheus listeners (#306). It is written from those components' goroutines
// (via recordComponentStop) and read from the /readyz handler goroutine, so
// every access is mutex-guarded.
type componentHealth struct {
	mu       sync.Mutex
	failures map[string]string // component name -> failure reason (its error string)
}

func newComponentHealth() *componentHealth {
	return &componentHealth{failures: make(map[string]string)}
}

// fail records that a component terminated with err. Callers must
// already have excluded clean-shutdown errors (see recordComponentStop /
// isCleanShutdownErr); a nil tracker is a no-op so the test seams that omit it
// stay valid.
func (h *componentHealth) fail(component string, err error) {
	if h == nil || err == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failures[component] = err.Error()
}

// reasons returns the current component failures as sorted "component: reason"
// strings (the shape readinessVerdict expects), or nil when none have failed.
// nil-safe for the same reason as fail.
func (h *componentHealth) reasons() []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.failures) == 0 {
		return nil
	}
	out := make([]string, 0, len(h.failures))
	for c, r := range h.failures {
		out = append(out, c+": "+r)
	}
	slices.Sort(out)
	return out
}

// readinessVerdict reports whether the service should be considered ready to
// receive traffic and, when it should not, a short human-readable reason. It
// is the pure decision function behind /readyz (see the readyz method and
// registerProbes in admin.go); keeping it separate from the HTTP plumbing
// makes every branch trivial to unit test without spinning up a server.
//
// Two conditions gate readiness (#57):
//   - the app is still starting up: at least one registered collector has not
//     completed its first tick yet (deriveHealth's "starting" verdict);
//   - an enabled component (a stream/webhook receiver, or the admin or
//     Prometheus listener) has terminally failed to bind or has stopped
//     unexpectedly (componentFailures is non-empty).
//
// A collector merely being "degraded" (occasional failures, overdue, or a
// stuck checkpoint) does NOT gate readiness on its own — only the two harder
// conditions above do, matching the issue's acceptance criteria. /healthz
// stays pure liveness and never consults this at all.
func readinessVerdict(collectors []statusdata.CollectorStatus, componentFailures []string) (ready bool, reason string) {
	if health, reasons := deriveHealth(collectors); health == healthStarting {
		return false, "starting: " + strings.Join(reasons, "; ")
	}
	if len(componentFailures) > 0 {
		return false, "component failure: " + strings.Join(componentFailures, "; ")
	}
	return true, ""
}

// readyz serves /readyz: 200 "ok" once the service is ready, otherwise 503
// with a short plain-text reason. See readinessVerdict for the gating rules.
//
// Live component-failure state comes from a.readyState, populated by
// recordComponentStop (internal/app/selfobs.go) when a receiver or a listener
// terminates with other than a clean-shutdown error (see isCleanShutdownErr).
func (a *App) readyz(w http.ResponseWriter, _ *http.Request) {
	ready := true
	reason := ""
	if a.ingressWAL != nil {
		state := a.ingressWAL.Health().State
		if state != ingressWALStateDisabled && state != ingressWALStateReady {
			ready = false
			reason = "ingress WAL: " + string(state)
		}
	}
	if ready {
		ready, reason = readinessVerdict(a.collectorStatuses(time.Now()), a.readyState.reasons())
	}
	w.Header().Set("Content-Type", "text/plain")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, reason)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}
