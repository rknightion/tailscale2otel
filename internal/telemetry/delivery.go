package telemetry

import (
	"context"
	"errors"
	"log/slog"
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
	// errClassPartialSuccess is a distinct, non-failure outcome (#382, #359): the
	// pinned OTLP exporters (otlpmetricgrpc/http, otlplog grpc/http, otlptrace
	// grpc/http, all v1.44.0/v0.20.0) report a backend's partial rejection as an
	// error whose text ALWAYS begins with the SDK's own fixed literal
	// "OTLP partial success: " even though the HTTP/gRPC call itself succeeded.
	// The rejected COUNT is not reachable from here (it sits behind an unexported
	// type in each exporter's own internal package, which this module cannot
	// import across the package boundary), so this only records THAT a partial
	// rejection happened, never how many items — see delivery_trace.go's sibling
	// comment and this issue's PARTIAL SUCCESS VERDICT for the full analysis.
	errClassPartialSuccess = "partial_success"
)

// exportErrorClasses is the closed set above, for tests and for anything that
// needs to bound the attribute's cardinality.
var exportErrorClasses = []string{
	errClassTimeout, errClassCanceled, errClassUnauthenticated,
	errClassRateLimited, errClassUnavailable, errClassInvalid, errClassOther,
	errClassPartialSuccess,
}

// partialSuccessPrefix is the OTel SDK's own fixed literal prefix on a partial
// success error (see errClassPartialSuccess). It is never influenced by the
// backend's free-text error message, which is interpolated AFTER this prefix, so
// matching on it cannot be spoofed into a false positive by backend content and
// never requires reading past this prefix into that backend text.
const partialSuccessPrefix = "otlp partial success:"

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
	// PartialSuccesses counts exports the backend accepted but partially rejected
	// (#382). Deliberately NOT folded into Failures/ConsecutiveFailures: some data
	// got through, so treating it as an outage would drag the admin verdict to
	// degraded over forward progress.
	PartialSuccesses     int64
	LastPartialSuccessAt time.Time
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

	// Diagnostics (#365) are late-bound via setDiagnostics, mirroring the
	// exportObserver pattern in export_counting.go: the tracker is constructed
	// before the Emitter/logger exist, so both start nil and every use is
	// nil-guarded. logger is always set once the app has one (diagnostics are not
	// gated on self-observability being enabled — an operator watching plain logs
	// still wants to know OTLP is down); emitter is set only when self-obs is on,
	// since it is the sole consumer of the suppression counter.
	logger        *slog.Logger
	emitter       Emitter
	providerLabel string // "" (process) or a tailnet name, for the log line only

	// episodes tracks one entry per (signal, error class) currently failing, so a
	// sustained outage logs its first occurrence, suppresses the rest (counting
	// them), and periodically summarizes — instead of one line per failed export.
	episodes map[deliveryEpisodeKey]*deliveryEpisode
}

// deliveryEpisodeKey is the bounded suppression key: at most
// len(signalOrder)*len(exportErrorClasses) entries can ever exist. Provider
// identity is NOT part of the key because each Provider owns its own
// deliveryTracker (one per tailnet plus one process-wide) — the tracker instance
// itself is already the provider boundary.
type deliveryEpisodeKey struct {
	signal string
	class  string
}

type deliveryEpisode struct {
	firstAt                time.Time
	lastSummaryAt          time.Time
	suppressedSinceSummary int64
}

// diagnosticsSummaryInterval bounds how often a sustained outage re-logs, so a
// backend down for hours produces a handful of summaries rather than silence or
// a flood. Not configurable (yet): see this issue's CONFIG REQUEST.
const diagnosticsSummaryInterval = 5 * time.Minute

func newDeliveryTracker() *deliveryTracker {
	t := &deliveryTracker{bySignal: make(map[string]*DeliveryState, len(signalOrder))}
	for _, s := range signalOrder {
		t.bySignal[s] = &DeliveryState{Signal: s}
	}
	return t
}

// setDiagnostics late-binds the logger, the Emitter (nil to disable the
// suppression counter, e.g. self-observability off), and an optional provider
// label used only to annotate log lines (never as part of the suppression key —
// see deliveryEpisodeKey). Safe to call once, before observe() is used
// concurrently.
func (t *deliveryTracker) setDiagnostics(logger *slog.Logger, e Emitter, providerLabel string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logger = logger
	t.emitter = e
	t.providerLabel = providerLabel
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
		class := classifyExportError(err)
		if class == errClassPartialSuccess {
			// Forward progress, not an outage: some data got through, so this must
			// not count toward Failures/ConsecutiveFailures or feed the outage
			// suppression/recovery machinery below.
			s.PartialSuccesses++
			s.LastPartialSuccessAt = now
			s.LastErrorClass = class
			s.ConsecutiveFailures = 0
			return
		}
		s.Failures++
		s.ConsecutiveFailures++
		s.LastFailureAt = now
		s.LastErrorClass = class
		t.recordFailureLocked(signal, class, err)
		return
	}
	s.ConsecutiveFailures = 0
	s.LastSuccessAt = now
	// LastFailureAt and LastErrorClass are deliberately NOT cleared: "recovered
	// two minutes ago after failing for an hour" is the thing an operator is
	// trying to establish, and clearing them erases it.
	t.recordRecoveryLocked(signal)
}

// recordFailureLocked implements the #365 suppression policy for one failed
// export. Called with t.mu already held. Self-observation METRICS stay exact
// (DeliveryState.Failures above is incremented unconditionally); only the LOG
// output and this function are rate-limited.
func (t *deliveryTracker) recordFailureLocked(signal, class string, err error) {
	if t.episodes == nil {
		t.episodes = make(map[deliveryEpisodeKey]*deliveryEpisode)
	}
	key := deliveryEpisodeKey{signal: signal, class: class}
	now := time.Now()
	ep, ok := t.episodes[key]
	if !ok {
		t.episodes[key] = &deliveryEpisode{firstAt: now, lastSummaryAt: now}
		t.logDiagnostic(slog.LevelWarn, "OTLP export failing", signal, class, "error", err)
		return
	}
	ep.suppressedSinceSummary++
	t.emitSuppressed(signal, class)
	if now.Sub(ep.lastSummaryAt) >= diagnosticsSummaryInterval {
		t.logDiagnostic(slog.LevelWarn, "OTLP export still failing",
			signal, class,
			"suppressed_since_last_summary", ep.suppressedSinceSummary,
			"failing_since", ep.firstAt.Format(time.RFC3339))
		ep.lastSummaryAt = now
		ep.suppressedSinceSummary = 0
	}
}

// recordRecoveryLocked logs exactly one "recovered" line the moment a signal
// that had an ongoing failure episode succeeds again, then clears every episode
// for that signal (across all error classes — the pipe is flowing again, so a
// stale class from earlier in the outage must not linger). A signal with no
// ongoing episode (the common case: a healthy signal succeeding) logs nothing.
func (t *deliveryTracker) recordRecoveryLocked(signal string) {
	if len(t.episodes) == 0 {
		return
	}
	var recovered bool
	for key := range t.episodes {
		if key.signal == signal {
			delete(t.episodes, key)
			recovered = true
		}
	}
	if recovered {
		t.logDiagnostic(slog.LevelInfo, "OTLP export recovered", signal, "")
	}
}

// logDiagnostic is the single place that writes an outage diagnostic line, so
// the provider label is attached consistently. class == "" (the recovery case)
// omits the error_class field. Called with t.mu held; args are extra key/value
// pairs appended after the fixed fields.
func (t *deliveryTracker) logDiagnostic(level slog.Level, msg, signal, class string, args ...any) {
	if t.logger == nil {
		return
	}
	fields := make([]any, 0, 6+len(args))
	fields = append(fields, "signal", signal)
	if class != "" {
		fields = append(fields, "error_class", class)
	}
	if t.providerLabel != "" {
		fields = append(fields, "tailnet", t.providerLabel)
	}
	fields = append(fields, args...)
	t.logger.Log(context.Background(), level, msg, fields...)
}

// emitSuppressed records one suppressed diagnostic log line as an exact counter
// (#365: "self-observation metrics stay exact" — suppression applies to LOGS
// only). Called with t.mu held; nil-guarded for self-obs off.
func (t *deliveryTracker) emitSuppressed(signal, class string) {
	if t.emitter == nil {
		return
	}
	EmitExportDiagnosticsSuppressed(t.emitter, signal, class)
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
	// Partial success next, and ahead of every substring check below: it is the
	// SDK's own fixed prefix (never backend-controlled — see errClassPartialSuccess),
	// but a rejected item's backend message could otherwise coincidentally match one
	// of those substrings (e.g. contain "timeout" or "429").
	if strings.Contains(msg, partialSuccessPrefix) {
		return errClassPartialSuccess
	}
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

// exportSignalError tags an OTLP export error with which signal produced it
// (#359). The process-wide otel.Handle(err) callback that InstallExportErrorHandler
// installs is invoked generically by the SDK's periodic reader / batch log
// processor / batch span processor with no signal argument at all, so without this
// the global handler cannot attribute tailscale2otel.export.failures by signal.
// Each of the three exporter wrappers (export_counting.go, delivery_trace.go)
// already knows its own signal, so they tag the error on the way out.
//
// Error()/Unwrap() delegate to the inner error unchanged: classifyExportError,
// errors.Is(err, context.DeadlineExceeded)/(sdkmetric.ErrInstrumentName), and the
// SDK's own generic otel.Handle(err) call all see exactly the same text and
// identity as before this wrap existed.
type exportSignalError struct {
	signal string
	err    error
}

func withExportSignal(signal string, err error) error {
	if err == nil {
		return nil
	}
	return &exportSignalError{signal: signal, err: err}
}

func (e *exportSignalError) Error() string { return e.err.Error() }
func (e *exportSignalError) Unwrap() error { return e.err }

// exportSignalOf extracts the signal tagged by withExportSignal, if any. Errors
// that never passed through one of the three wrapped exporters (e.g. an
// instrument-creation error) report ok == false.
func exportSignalOf(err error) (string, bool) {
	var se *exportSignalError
	if errors.As(err, &se) {
		return se.signal, true
	}
	return "", false
}

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
			m.PartialSuccesses += st.PartialSuccesses
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
			if st.LastPartialSuccessAt.After(m.LastPartialSuccessAt) {
				m.LastPartialSuccessAt = st.LastPartialSuccessAt
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
