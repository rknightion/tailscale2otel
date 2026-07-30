package webhook

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// -----------------------------------------------------------------------------
// GHSA-9547-8jpc-48h6 aggregate admission control
//
// Ported from the streaming receiver's #209 fix (internal/stream/stream.go,
// internal/stream/hardening_test.go). The webhook receiver has the identical
// shape of problem: MaxBodyBytes bounds ONE request's body, not the sum of
// every body in flight, and that buffering happens BEFORE HMAC verification —
// reachable with no credential — so many unauthenticated connections multiply
// the per-request allowance. An aggregate admission slot, taken before any
// body read, closes that gap.
// -----------------------------------------------------------------------------

// newLimitTestServer builds a Server wired to a fresh Recorder with the given
// MaxConcurrentRequests. No Secret is configured: admission control must
// reject an over-budget request before signature verification would even run,
// so these tests intentionally never sign a body.
func newLimitTestServer(t *testing.T, maxConcurrent int) (*Server, *telemetrytest.Recorder) {
	t.Helper()
	rec := telemetrytest.New()
	s := New(Options{
		Listen:                "127.0.0.1:0",
		Path:                  "/webhook",
		MaxConcurrentRequests: maxConcurrent,
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, rec
}

// blockingBody is a request body whose first Read blocks until release is
// closed, pinning a handler inside readBody so a second request meets a full
// admission semaphore.
type blockingBody struct {
	reading chan struct{} // closed on the first Read
	release chan struct{} // closed by the test to let the body complete
	once    bool
	rest    io.Reader
}

func newBlockingBody(payload string) *blockingBody {
	return &blockingBody{
		reading: make(chan struct{}),
		release: make(chan struct{}),
		rest:    strings.NewReader(payload),
	}
}

func (b *blockingBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.reading)
		<-b.release
	}
	return b.rest.Read(p)
}

// postBody sends a POST with an arbitrary body reader, no signature header,
// and returns the recorded response.
func postBody(h http.Handler, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", body)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw
}

// TestAdmissionControl_BeyondLimitRejectedWith503WithoutReadingBody is the
// GHSA-9547-8jpc-48h6 control: with the budget full, a further request is
// refused with 503 + Retry-After instead of buffering another body — and its
// body is NEVER read (proven by the blocking reader never unblocking on its
// own) — so aggregate memory stays bounded no matter how many unauthenticated
// senders arrive at once. This fails against the pre-fix code, which had no
// admission control and would hang reading the second body instead of
// rejecting it outright.
func TestAdmissionControl_BeyondLimitRejectedWith503WithoutReadingBody(t *testing.T) {
	s, rec := newLimitTestServer(t, 1)
	h := s.Handler()

	first := newBlockingBody(twoEventBody)
	done := make(chan int, 1)
	go func() {
		w := postBody(h, first)
		done <- w.Code
	}()
	<-first.reading // the first handler now holds the only admission slot

	second := newBlockingBody(twoEventBody)
	w := postBody(h, second)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request status = %d, want 503; body=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\"", got)
	}
	select {
	case <-second.reading:
		t.Fatal("second request's body was read before admission was checked")
	default:
	}

	pts := rec.MetricPoints("tailscale.webhook.rejected")
	found := false
	for _, p := range pts {
		if p.Attrs["reason"] == "overloaded" {
			found = true
			if p.Value != 1 {
				t.Fatalf("rejected{reason=overloaded} = %v, want 1", p.Value)
			}
		}
	}
	if !found {
		t.Fatalf("no rejected{reason=overloaded} counter point; got %+v", pts)
	}

	close(first.release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", code)
	}
}

// TestAdmissionControl_SlotReleasedAfterRequest confirms the semaphore is
// released on the way out, so a burst does not permanently wedge the
// receiver.
func TestAdmissionControl_SlotReleasedAfterRequest(t *testing.T) {
	s, _ := newLimitTestServer(t, 1)
	h := s.Handler()

	for i := range 3 {
		w := postBody(h, strings.NewReader(twoEventBody))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 (slot not released?); body=%q", i, w.Code, w.Body.String())
		}
	}
}

// TestAdmissionControl_NegativeDisablesLimit pins the documented escape
// hatch: a negative MaxConcurrentRequests turns the budget off entirely.
func TestAdmissionControl_NegativeDisablesLimit(t *testing.T) {
	s, _ := newLimitTestServer(t, -1)
	h := s.Handler()

	first := newBlockingBody(twoEventBody)
	done := make(chan int, 1)
	go func() {
		w := postBody(h, first)
		done <- w.Code
	}()
	<-first.reading

	w := postBody(h, strings.NewReader(twoEventBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the limit disabled; body=%q", w.Code, w.Body.String())
	}

	close(first.release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", code)
	}
}

// TestFrozenDefaultMaxConcurrentRequests pins the default admission budget to
// the value agreed for GHSA-9547-8jpc-48h6 (matching the streaming receiver's
// #209 default), so a change here is a conscious edit to the receiver's
// advertised safety envelope rather than a drive-by tweak.
func TestFrozenDefaultMaxConcurrentRequests(t *testing.T) {
	if defaultMaxConcurrentRequests != 4 {
		t.Errorf("defaultMaxConcurrentRequests = %d, want 4", defaultMaxConcurrentRequests)
	}
}
