package app

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/coordination"
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
// disabled, ready, or merely replaying. The WAL reports a state machine rather
// than a terminal error, so it cannot go through componentHealth; this is where
// the two shapes meet.
//
// replaying is deliberately NOT a failure. It is the state EVERY drain cycle
// passes through — ingressWALCoordinator.Run sets it on each pass and clears it
// again on success — so treating it as one made /readyz flap 503 against the
// live worker's duty cycle, and marked the component Failed on the status page
// for normal work. On a leader promoted onto a large inherited backlog it held
// 503 for the whole drain, which pulled the leader-labeled pod out of the
// receiver and admin Services and refused every inbound record on the one pod
// allowed to accept them. A startup drain still gates readiness, but through
// ingressWALStartupReason, which says so honestly and is bounded.
func (a *App) ingressWALFailure() string {
	if a.ingressWAL == nil {
		return ""
	}
	state := a.ingressWAL.Health().State
	switch state {
	case ingressWALStateDisabled, ingressWALStateReady, ingressWALStateReplaying:
		return ""
	}
	return appcatalog.ComponentIngressWAL + ": " + string(state)
}

// ingressWALStartupReason returns a readiness reason while the startup drain is
// still holding receiver startup, or "" once the receivers may open.
//
// This is NOT a component failure and never reaches componentFailureReasons:
// the WAL is working, and the status page must not call it failed. It is a
// readiness reason only, because the stream and webhook listeners are not open
// yet and a leader-labeled pod must not be routed traffic it would refuse.
// startIngressWAL bounds how long it can stay non-empty.
func (a *App) ingressWALStartupReason() string {
	if !a.walStartupPending.Load() || a.ingressWAL == nil {
		return ""
	}
	health := a.ingressWAL.Health().WAL
	return fmt.Sprintf(
		"%s: draining startup backlog (%d entries, %d bytes)",
		appcatalog.ComponentIngressWAL, health.PendingEntries, health.PendingBytes,
	)
}

// activeReadiness is the readiness verdict for a process doing the active
// lifecycle's work: the singleton in coordination.mode=none and the Lease
// holder in kubernetes mode. Standbys take their own branch in readyz.
//
// Order matters only in which reason is REPORTED: a hard WAL failure outranks a
// startup drain, which outranks "still starting", because that is the order in
// which they are actionable.
func (a *App) activeReadiness() (bool, string) {
	if wal := a.ingressWALFailure(); wal != "" {
		return false, wal
	}
	if startup := a.ingressWALStartupReason(); startup != "" {
		return false, startup
	}
	return readinessVerdict(a.collectorStatuses(time.Now()), a.readyState.reasons())
}

// readyz serves /readyz: 200 "ok" once the service is ready, otherwise 503
// with a short plain-text reason. See readinessVerdict for the gating rules and
// componentFailureReasons for the state behind them.
//
// The active-lifecycle branches share activeReadiness, which checks the WAL
// before readinessVerdict so a WAL failure is the reported reason even while
// collectors are still starting: buffered ingress that is not draining is the
// more actionable fact, and "starting" would hide it behind a condition that
// resolves on its own. Which reason is reported FIRST is the only thing that
// ordering decides — the status page derives its verdict from the same
// componentFailureReasons list.
func (a *App) readyz(w http.ResponseWriter, _ *http.Request) {
	var ready bool
	var reason string
	if a.cfg != nil && a.cfg.Coordination.Mode == "kubernetes" {
		switch a.currentCoordination().State {
		case "":
			ready, reason = false, "coordination: not started"
		case coordination.StateStandby:
			// Collectors and WAL replay are leader-only work, so a campaigning
			// standby cannot satisfy either startup gate. Its live listeners
			// still retain their component-failure gating.
			ready, reason = readinessVerdict(nil, a.readyState.reasons())
		default:
			ready, reason = a.activeReadiness()
		}
	} else {
		ready, reason = a.activeReadiness()
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
