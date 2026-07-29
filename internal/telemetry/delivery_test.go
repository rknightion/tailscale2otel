package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// The admin page labeled a field "last export" and computed it from the
// freshest COLLECTOR success, so it could report a recent export while every
// OTLP request was failing (#317). The exporters already knew the truth — the
// outcome was observed and thrown at a histogram — so this records it.

func TestDeliveryTracksSuccessAndFailurePerSignal(t *testing.T) {
	d := newDeliveryTracker()
	d.observe(SignalMetrics, nil, 0.25)
	d.observe(SignalLogs, errors.New("rpc error: code = Unavailable desc = connection refused"), 1.5)

	byName := map[string]DeliveryState{}
	for _, s := range d.states() {
		byName[s.Signal] = s
	}

	m := byName[SignalMetrics]
	if m.Exports != 1 || m.Failures != 0 || m.ConsecutiveFailures != 0 {
		t.Errorf("metrics = %+v, want one export, no failures", m)
	}
	if m.LastSuccessAt.IsZero() {
		t.Error("a successful export left no last-success time, which is the field the page shows")
	}
	if !m.LastFailureAt.IsZero() {
		t.Error("a successful export recorded a failure time")
	}
	if m.LastDurationSeconds != 0.25 {
		t.Errorf("last duration = %v, want 0.25", m.LastDurationSeconds)
	}

	l := byName[SignalLogs]
	if l.Exports != 1 || l.Failures != 1 || l.ConsecutiveFailures != 1 {
		t.Errorf("logs = %+v, want one export, one failure", l)
	}
	if l.LastErrorClass != errClassUnavailable {
		t.Errorf("logs error class = %q, want %q", l.LastErrorClass, errClassUnavailable)
	}
	// The signals must not be pooled: a healthy metric pipeline beside a broken
	// log pipeline is the exact state this has to be able to show.
	if !l.LastSuccessAt.IsZero() {
		t.Error("the failing log signal inherited a success time from the metric signal")
	}
}

func TestDeliveryConsecutiveFailuresResetOnSuccess(t *testing.T) {
	d := newDeliveryTracker()
	for range 3 {
		d.observe(SignalMetrics, errors.New("context deadline exceeded"), 5)
	}
	if got := d.states()[0].ConsecutiveFailures; got != 3 {
		t.Fatalf("consecutive failures = %d, want 3", got)
	}
	d.observe(SignalMetrics, nil, 0.1)
	s := d.states()[0]
	if s.ConsecutiveFailures != 0 {
		t.Errorf("consecutive failures = %d after a success, want 0", s.ConsecutiveFailures)
	}
	if s.Failures != 3 {
		t.Errorf("lifetime failures = %d, want 3: a success clears the streak, not the history", s.Failures)
	}
	if s.LastFailureAt.IsZero() {
		t.Error("the last failure time was cleared by a later success; an operator still needs it")
	}
}

// The class is what reaches the admin page, so it must be a CLOSED set that
// never carries the backend's response body or a credential.
func TestClassifyExportError(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{context.DeadlineExceeded, errClassTimeout},
		{errors.New("context deadline exceeded"), errClassTimeout},
		{context.Canceled, errClassCanceled},
		{errors.New("rpc error: code = Unauthenticated desc = bad token"), errClassUnauthenticated},
		{errors.New("failed to upload metrics: 401 Unauthorized"), errClassUnauthenticated},
		{errors.New("failed to upload: 403 Forbidden"), errClassUnauthenticated},
		{errors.New("rpc error: code = PermissionDenied"), errClassUnauthenticated},
		{errors.New("rpc error: code = ResourceExhausted desc = too many"), errClassRateLimited},
		{errors.New("failed to upload logs: 429 Too Many Requests"), errClassRateLimited},
		{errors.New("rpc error: code = Unavailable desc = connection refused"), errClassUnavailable},
		{errors.New("dial tcp 10.0.0.1:4317: connect: connection refused"), errClassUnavailable},
		{errors.New("failed to upload: 503 Service Unavailable"), errClassUnavailable},
		{errors.New("rpc error: code = InvalidArgument desc = bad resource"), errClassInvalid},
		{errors.New("failed to upload metrics: 400 Bad Request"), errClassInvalid},
		{errors.New("something nobody has seen before"), errClassOther},
	}
	for _, tc := range tests {
		if got := classifyExportError(tc.err); got != tc.want {
			t.Errorf("classifyExportError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// A backend that echoes the request, or an error carrying a signed URL, must
// not reach an operator's screen through this field.
func TestClassifyExportErrorNeverEchoesTheError(t *testing.T) {
	secret := "Bearer glc_eyJvIjoiMTIzNDU2Nzg5MCJ9"
	err := errors.New("failed to upload metrics: 401 Unauthorized: " + secret)
	got := classifyExportError(err)
	if got != errClassUnauthenticated {
		t.Fatalf("class = %q, want %q", got, errClassUnauthenticated)
	}
	for _, cls := range exportErrorClasses {
		if len(cls) == 0 {
			t.Fatal("empty class in the closed set")
		}
	}
	// The closed set is what bounds this: anything not in it would be text from
	// the backend, and the class is rendered verbatim on the admin page.
	var known bool
	for _, cls := range exportErrorClasses {
		if cls == got {
			known = true
		}
	}
	if !known {
		t.Errorf("class %q is outside the closed set %v, so the page could render backend text", got, exportErrorClasses)
	}
}

func TestDeliveryStatesAreStableAndComplete(t *testing.T) {
	d := newDeliveryTracker()
	got := d.states()
	if len(got) != 3 {
		t.Fatalf("states() returned %d rows, want one per signal (metrics, logs, traces)", len(got))
	}
	want := []string{SignalMetrics, SignalLogs, SignalTraces}
	for i, s := range got {
		if s.Signal != want[i] {
			t.Errorf("row %d = %q, want %q: the order is what the page renders", i, s.Signal, want[i])
		}
		if s.Exports != 0 {
			t.Errorf("row %q reports %d exports before anything happened", s.Signal, s.Exports)
		}
	}
}

// A backend that rejects some but not all items returns HTTP/gRPC success, so
// the exporter surfaces the rejection as an error whose text always begins with
// the SDK's own fixed "OTLP partial success:" prefix (never a backend-controlled
// prefix — see the PARTIAL SUCCESS VERDICT in the delivery notes). #382 says model
// it as an explicit outcome alongside the closed errClass* set, not a second
// upstream-flavored taxonomy.
func TestClassifyExportErrorPartialSuccess(t *testing.T) {
	err := errors.New("failed to upload metrics: OTLP partial success: duplicate label \"x\" (3 metric data points rejected)")
	if got := classifyExportError(err); got != errClassPartialSuccess {
		t.Fatalf("class = %q, want %q", got, errClassPartialSuccess)
	}
}

// A partial success is forward progress, not an outage: it must not count as a
// Failure/ConsecutiveFailure (that would fold "mostly delivered" into the same
// bucket as "nothing got through" and could wrongly drag the admin verdict to
// degraded), but it must be visible as its own count.
func TestDeliveryPartialSuccessIsNotAFailure(t *testing.T) {
	d := newDeliveryTracker()
	d.observe(SignalMetrics, errors.New("context deadline exceeded"), 1)
	d.observe(SignalMetrics, errors.New("OTLP partial success: msg (2 metric data points rejected)"), 0.5)

	s := d.states()[0]
	if s.Failures != 1 {
		t.Errorf("Failures = %d, want 1 (the timeout only)", s.Failures)
	}
	if s.PartialSuccesses != 1 {
		t.Errorf("PartialSuccesses = %d, want 1", s.PartialSuccesses)
	}
	if s.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0: the partial success is forward progress and resets the streak", s.ConsecutiveFailures)
	}
	if s.LastErrorClass != errClassPartialSuccess {
		t.Errorf("LastErrorClass = %q, want %q", s.LastErrorClass, errClassPartialSuccess)
	}
	if s.LastPartialSuccessAt.IsZero() {
		t.Error("LastPartialSuccessAt was not recorded")
	}
}

// #365: a sustained outage must not log once per failed export. The tracker logs
// the first failure of an episode, suppresses the rest (counting them), and
// periodically emits a summary — all keyed on signal+error class so an outage on
// one signal never masks or is masked by another.
func TestDeliverySuppressesRepeatedFailureLogs(t *testing.T) {
	d := newDeliveryTracker()
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d.setDiagnostics(logger, nil, "")

	for range 50 {
		d.observe(SignalLogs, errors.New("rpc error: code = Unavailable desc = connection refused"), 0.1)
	}

	lines := strings.Count(buf.String(), "\n")
	if lines == 0 {
		t.Fatal("no diagnostic was logged for the first failure")
	}
	if lines >= 50 {
		t.Fatalf("logged %d lines for 50 failures of the same (signal, class): log rate is not bounded", lines)
	}
	if !strings.Contains(buf.String(), "connection refused") {
		t.Error("the first failure's diagnostic dropped the underlying error detail")
	}
}

// Exactly one recovery log when a failing signal succeeds again, and none when
// it never failed in the first place (a healthy signal must not spam "recovered"
// on every ordinary success).
func TestDeliveryLogsRecoveryExactlyOnce(t *testing.T) {
	d := newDeliveryTracker()
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d.setDiagnostics(logger, nil, "")

	d.observe(SignalMetrics, nil, 0.1) // healthy success: no episode, no recovery log
	if buf.Len() != 0 {
		t.Fatalf("a plain success logged something: %q", buf.String())
	}

	for range 5 {
		d.observe(SignalMetrics, errors.New("context deadline exceeded"), 1)
	}
	beforeRecovery := buf.String()

	d.observe(SignalMetrics, nil, 0.2) // recovers
	afterFirstRecovery := buf.String()
	if !strings.Contains(afterFirstRecovery, "recovered") {
		t.Fatalf("no recovery line logged; got: %q", afterFirstRecovery)
	}
	if strings.Count(afterFirstRecovery, "recovered") != 1 {
		t.Fatalf("recovery logged %d times, want exactly 1", strings.Count(afterFirstRecovery, "recovered"))
	}
	_ = beforeRecovery

	d.observe(SignalMetrics, nil, 0.1) // stays healthy: no additional recovery line
	if strings.Count(buf.String(), "recovered") != 1 {
		t.Fatalf("a second healthy success logged another recovery line: %q", buf.String())
	}
}

// A different signal's outage must never suppress or fake-recover this signal's
// episode: the key is (signal, class), not a global switch.
func TestDeliverySuppressionIsPerSignal(t *testing.T) {
	d := newDeliveryTracker()
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d.setDiagnostics(logger, nil, "")

	d.observe(SignalLogs, errors.New("context deadline exceeded"), 1)
	d.observe(SignalMetrics, nil, 0.1) // a healthy, unrelated signal succeeding
	if strings.Contains(buf.String(), "recovered") {
		t.Fatalf("metrics succeeding falsely reported logs as recovered: %q", buf.String())
	}
}

func TestDeliveryIsSafeForConcurrentUse(t *testing.T) {
	d := newDeliveryTracker()
	done := make(chan struct{})
	go func() {
		for range 200 {
			d.observe(SignalMetrics, nil, 0.1)
		}
		close(done)
	}()
	for range 200 {
		_ = d.states()
	}
	<-done
	if got := d.states()[0].Exports; got != 200 {
		t.Errorf("exports = %d, want 200", got)
	}
	_ = time.Now
}
