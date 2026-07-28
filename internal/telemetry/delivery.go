package telemetry

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Signal names for the three OTLP pipelines. They are the exportObserver's
// first argument and the DeliveryState key, so they are also what the admin
// page renders.
const (
	SignalMetrics = "metrics"
	SignalLogs    = "logs"
	SignalTraces  = "traces"
)

// signalOrder fixes the order states() returns, so the admin table does not
// reshuffle between polls.
var signalOrder = []string{SignalMetrics, SignalLogs, SignalTraces}

// Export-error classes. A CLOSED set, for two reasons: it is rendered verbatim
// on the admin page, and an OTLP export error can carry the backend's response
// body — which is exactly where a signed URL or an echoed credential would be.
// Classifying rather than forwarding means no backend text ever reaches the
// page (#317).
const (
	errClassTimeout         = "timeout"
	errClassCanceled        = "canceled"
	errClassUnauthenticated = "unauthenticated"
	errClassRateLimited     = "rate_limited"
	errClassUnavailable     = "unavailable"
	errClassInvalid         = "invalid"
	errClassOther           = "other"
)

// exportErrorClasses is the closed set above, for tests and for anything that
// needs to bound the attribute's cardinality.
var exportErrorClasses = []string{
	errClassTimeout, errClassCanceled, errClassUnauthenticated,
	errClassRateLimited, errClassUnavailable, errClassInvalid, errClassOther,
}

// DeliveryState is one signal's OTLP delivery history: what the exporter
// actually shipped, as opposed to what the collectors produced.
//
// The distinction is the whole point. "Emitted" (ExportStats) counts what was
// handed to an exporter; this counts what came back. The page used to show a
// collector-success timestamp under the label "last export", so a completely
// broken OTLP pipeline still read as recent delivery.
type DeliveryState struct {
	Signal string
	// Exports is every completed attempt; Failures is how many of them failed.
	Exports  int64
	Failures int64
	// ConsecutiveFailures is the current unbroken failure streak, reset by any
	// success. It is what distinguishes a blip from an outage.
	ConsecutiveFailures int64
	LastSuccessAt       time.Time
	LastFailureAt       time.Time
	// LastDurationSeconds is the wall-clock duration of the most recent attempt,
	// successful or not.
	LastDurationSeconds float64
	// LastErrorClass is one of exportErrorClasses, or "" if nothing has failed.
	// Never the error text.
	LastErrorClass string
}

// deliveryTracker records completed exports per signal. Written from the
// exporter goroutines and read from the admin handler, so every access is
// mutex-guarded.
//
// Unlike the export COUNTERS, this is populated whether or not
// self-observability is enabled: the admin page is an operator's local view and
// has to work on a deployment that exports no self-telemetry at all.
type deliveryTracker struct {
	mu       sync.Mutex
	bySignal map[string]*DeliveryState
}

func newDeliveryTracker() *deliveryTracker {
	t := &deliveryTracker{bySignal: make(map[string]*DeliveryState, len(signalOrder))}
	for _, s := range signalOrder {
		t.bySignal[s] = &DeliveryState{Signal: s}
	}
	return t
}

// observe records one completed export. A nil tracker is a no-op, so the test
// seams that construct exporters directly stay valid.
func (t *deliveryTracker) observe(signal string, err error, seconds float64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.bySignal[signal]
	if !ok {
		return
	}
	now := time.Now()
	s.Exports++
	s.LastDurationSeconds = seconds
	if err != nil {
		s.Failures++
		s.ConsecutiveFailures++
		s.LastFailureAt = now
		s.LastErrorClass = classifyExportError(err)
		return
	}
	s.ConsecutiveFailures = 0
	s.LastSuccessAt = now
	// LastFailureAt and LastErrorClass are deliberately NOT cleared: "recovered
	// two minutes ago after failing for an hour" is the thing an operator is
	// trying to establish, and clearing them erases it.
}

// states returns a copy of every signal's state in signalOrder, including
// signals that have never exported — a pipeline that has produced nothing at
// all is a finding, not a row to omit.
func (t *deliveryTracker) states() []DeliveryState {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]DeliveryState, 0, len(signalOrder))
	for _, name := range signalOrder {
		if s := t.bySignal[name]; s != nil {
			out = append(out, *s)
		}
	}
	return out
}

// classifyExportError maps an exporter error onto the closed class set. It
// reads the error's TEXT because the OTLP exporters return gRPC status errors
// and HTTP status errors through the same interface, and matching on both
// shapes here avoids a direct grpc dependency for a display string.
//
// It returns only a constant from the set — never any part of err — so a
// response body echoed by the backend cannot reach the page.
func classifyExportError(err error) string {
	if err == nil {
		return ""
	}
	// Sentinel checks first: these are exact and cannot be confused by a
	// backend that happens to mention "deadline" in its response.
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return errClassTimeout
	case errors.Is(err, context.Canceled):
		return errClassCanceled
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timeout"):
		return errClassTimeout
	case strings.Contains(msg, "context canceled"), strings.Contains(msg, "code = canceled"):
		return errClassCanceled
	case strings.Contains(msg, "unauthenticated"), strings.Contains(msg, "permissiondenied"),
		strings.Contains(msg, "permission denied"), strings.Contains(msg, "401"),
		strings.Contains(msg, "403"):
		return errClassUnauthenticated
	case strings.Contains(msg, "resourceexhausted"), strings.Contains(msg, "429"):
		return errClassRateLimited
	case strings.Contains(msg, "unavailable"), strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such host"), strings.Contains(msg, "503"):
		return errClassUnavailable
	case strings.Contains(msg, "invalidargument"), strings.Contains(msg, "400"),
		strings.Contains(msg, "outofrange"):
		return errClassInvalid
	default:
		return errClassOther
	}
}

// deliveryLimit is the failure streak at which a signal is treated as a
// sustained export failure rather than a blip. Three consecutive failed
// exports at the default metric interval is several minutes of nothing
// reaching the backend.
const deliveryLimit = 3

// Failing reports whether this signal has failed consistently enough to be
// worth surfacing on the admin health verdict.
func (s DeliveryState) Failing() bool { return s.ConsecutiveFailures >= deliveryLimit }

// Delivery merges every provider's delivery state into one per-signal view.
//
// One process's exporters all point at the same backend, so "is OTLP working"
// is a process-level question and one row per signal is what an operator is
// asking about. Counts sum; the newest success and failure win; the streak is
// the WORST across providers, because one tailnet's pipeline failing is a
// failure even while another's succeeds — an average would hide it.
func (s *ProviderSet) Delivery() []DeliveryState {
	merged := make(map[string]*DeliveryState, len(signalOrder))
	for _, name := range signalOrder {
		merged[name] = &DeliveryState{Signal: name}
	}
	fold := func(p *Provider) {
		if p == nil {
			return
		}
		for _, st := range p.Delivery() {
			m := merged[st.Signal]
			if m == nil {
				continue
			}
			m.Exports += st.Exports
			m.Failures += st.Failures
			if st.ConsecutiveFailures > m.ConsecutiveFailures {
				m.ConsecutiveFailures = st.ConsecutiveFailures
			}
			if st.LastSuccessAt.After(m.LastSuccessAt) {
				m.LastSuccessAt = st.LastSuccessAt
				m.LastDurationSeconds = st.LastDurationSeconds
			}
			if st.LastFailureAt.After(m.LastFailureAt) {
				m.LastFailureAt = st.LastFailureAt
				m.LastErrorClass = st.LastErrorClass
			}
		}
	}
	fold(s.process)
	for _, name := range s.order {
		fold(s.tailnet[name])
	}
	out := make([]DeliveryState, 0, len(signalOrder))
	for _, name := range signalOrder {
		out = append(out, *merged[name])
	}
	return out
}
