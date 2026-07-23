package stream

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFrozenLimits pins the hardening constants to the values agreed in the
// security-fix plan. They are deliberately not configurable, so a change here is
// a change to the receiver's advertised safety envelope and should be a
// conscious edit rather than a drive-by tweak. The external hardening tests
// restate maxRecordsPerRequest; this is what keeps the two in step.
func TestFrozenLimits(t *testing.T) {
	if maxRecordsPerRequest != 500_000 {
		t.Errorf("maxRecordsPerRequest = %d, want 500000", maxRecordsPerRequest)
	}
	if maxUnwrapDepth != 4 {
		t.Errorf("maxUnwrapDepth = %d, want 4", maxUnwrapDepth)
	}
	if handlerProcessDeadline != 30*time.Second {
		t.Errorf("handlerProcessDeadline = %s, want 30s", handlerProcessDeadline)
	}
	if defaultMaxConcurrentRequests != 4 {
		t.Errorf("defaultMaxConcurrentRequests = %d, want 4", defaultMaxConcurrentRequests)
	}
}

// TestWithProcessDeadline_BoundsTheResponse exercises the deadline wrapper with a
// millisecond budget instead of the real 30s one. It asserts what the wrapper can
// actually promise — a bounded RESPONSE — not that the slow handler is stopped
// (it is not; see the comment on Handler).
func TestWithProcessDeadline_BoundsTheResponse(t *testing.T) {
	done := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	h := withProcessDeadline(slow, time.Millisecond)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/services/collector/event", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 once the deadline elapses", w.Code)
	}
	if !strings.Contains(w.Body.String(), "deadline") {
		t.Fatalf("body = %q, want it to mention the deadline", w.Body.String())
	}
	<-done // let the slow handler finish so the race detector sees a clean exit
}

// TestHandler_AppliesProcessDeadline guards that Handler() — not just Run() —
// applies the deadline, so httptest-driven callers get the same bound as a real
// listener.
func TestHandler_AppliesProcessDeadline(t *testing.T) {
	s := &Server{path: defaultPath}
	if _, bare := s.Handler().(*http.ServeMux); bare {
		t.Fatal("Handler() returned a bare *http.ServeMux; the process deadline is not applied")
	}
}
