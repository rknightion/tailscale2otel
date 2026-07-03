package app

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/internal/app/statusdata"
)

// receiverHealth tracks terminal failures of the optional stream/webhook
// receivers so /readyz can report the service unready once a receiver has
// failed to bind or stopped unexpectedly (#57). It is written from the receiver
// goroutines (via recordReceiverStop) and read from the /readyz handler
// goroutine, so every access is mutex-guarded.
type receiverHealth struct {
	mu       sync.Mutex
	failures map[string]string // component name -> failure reason (its error string)
}

func newReceiverHealth() *receiverHealth {
	return &receiverHealth{failures: make(map[string]string)}
}

// fail records that a receiver component terminated with err. Callers must
// already have excluded clean-shutdown errors (see recordReceiverStop /
// isCleanShutdownErr); a nil tracker is a no-op so the test seams that omit it
// stay valid.
func (h *receiverHealth) fail(component string, err error) {
	if h == nil || err == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failures[component] = err.Error()
}

// reasons returns the current receiver failures as sorted "component: reason"
// strings (the shape readinessVerdict expects), or nil when none have failed.
// nil-safe for the same reason as fail.
func (h *receiverHealth) reasons() []string {
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
//   - an enabled receiver (stream/webhook) has terminally failed to bind or
//     has stopped unexpectedly (receiverFailures is non-empty).
//
// A collector merely being "degraded" (occasional failures, overdue, or a
// stuck checkpoint) does NOT gate readiness on its own — only the two harder
// conditions above do, matching the issue's acceptance criteria. /healthz
// stays pure liveness and never consults this at all.
func readinessVerdict(collectors []statusdata.CollectorStatus, receiverFailures []string) (ready bool, reason string) {
	if health, reasons := deriveHealth(collectors); health == healthStarting {
		return false, "starting: " + strings.Join(reasons, "; ")
	}
	if len(receiverFailures) > 0 {
		return false, "receiver failure: " + strings.Join(receiverFailures, "; ")
	}
	return true, ""
}

// readyz serves /readyz: 200 "ok" once the service is ready, otherwise 503
// with a short plain-text reason. See readinessVerdict for the gating rules.
//
// Live receiver-failure state comes from a.readyState, populated by
// recordReceiverStop (internal/app/selfobs.go) when a stream/webhook receiver
// terminates with other than a clean-shutdown error (see isCleanShutdownErr).
func (a *App) readyz(w http.ResponseWriter, _ *http.Request) {
	ready, reason := readinessVerdict(a.collectorStatuses(time.Now()), a.readyState.reasons())
	w.Header().Set("Content-Type", "text/plain")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, reason)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}
