package telemetry

import (
	"context"
	"errors"
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
