package app

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/coordination"
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
	// nil, not componentFailures: this call asks deriveHealth ONLY whether the
	// collectors are still starting. Passing the failures in would make it
	// return "degraded" (a failure outranks a pending first tick), the
	// starting branch would never fire, and the precedence below would invert.
	if health, reasons := deriveHealth(collectors, nil); health == healthStarting {
		return false, "starting: " + strings.Join(reasons, "; ")
	}
	if len(componentFailures) > 0 {
		return false, "component failure: " + strings.Join(componentFailures, "; ")
	}
	return true, ""
}

// componentFailureReasons is THE list of failed non-collector components, as
// sorted "component: reason" strings. Both /readyz and the status page's health
// verdict read it, which is what stops the probe and the page disagreeing about
// identical state (#318).
//
// Two sources feed it: a.readyState, populated by recordComponentStop
// (internal/app/selfobs.go) when a receiver or a listener terminates with other
// than a clean-shutdown error, and the ingress WAL, which carries its own state
// machine rather than a terminal error. Any WAL state other than disabled or
// ready means writes are not making it through, so it counts.
func (a *App) componentFailureReasons() []string {
	reasons := a.readyState.reasons()
	if f := a.ingressWALFailure(); f != "" {
		reasons = append(reasons, f)
	}
	return reasons
}

// ingressWALFailure returns the WAL's failure reason in the same
// "component: reason" shape as componentHealth.reasons, or "" when the WAL is
// disabled or ready. The WAL reports a state machine rather than a terminal
// error, so it cannot go through componentHealth; this is where the two shapes
// meet.
func (a *App) ingressWALFailure() string {
	if a.ingressWAL == nil {
		return ""
	}
	state := a.ingressWAL.Health().State
	if state == ingressWALStateDisabled || state == ingressWALStateReady {
		return ""
	}
	return appcatalog.ComponentIngressWAL + ": " + string(state)
}

// readyz serves /readyz: 200 "ok" once the service is ready, otherwise 503
// with a short plain-text reason. See readinessVerdict for the gating rules and
// componentFailureReasons for the state behind them.
//
// The WAL is checked before readinessVerdict, so a WAL failure is the reported
// reason even while collectors are still starting: buffered ingress that is not
// draining is the more actionable fact, and "starting" would hide it behind a
// condition that resolves on its own. Which reason is reported FIRST is the
// only thing this ordering decides — the status page derives its verdict from
// the same componentFailureReasons list.
func (a *App) readyz(w http.ResponseWriter, _ *http.Request) {
	var ready bool
	var reason string
	if a.cfg != nil && a.cfg.Coordination.Mode == "kubernetes" && a.currentCoordination().State != coordination.StateLeader {
		ready, reason = false, "coordination: "+string(a.currentCoordination().State)
	} else if wal := a.ingressWALFailure(); wal != "" {
		ready, reason = false, wal
	} else {
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
