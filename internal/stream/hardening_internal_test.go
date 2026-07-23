package stream

import (
	"io"
	"net"
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

// TestServerTimeouts_WriteWindowOutlastsProcessDeadline is the #232 regression
// guard. The process deadline's 503 is only reachable if the connection's write
// window is still open when TimeoutHandler fires.
//
// The margin needed is NOT just handlerProcessDeadline. http.Server starts the
// write deadline when the request's headers begin arriving, but TimeoutHandler's
// timer starts when ServeHTTP is entered — and those are separated by up to
// ReadHeaderTimeout. So the worst case is a client that dawdles over its headers
// for the full ReadHeaderTimeout and only then trips the deadline, and the write
// window has to outlast BOTH. Equal values (the pre-fix 30s/30s) always lose the
// race; so would the "just lower the deadline a bit" fix.
func TestServerTimeouts_WriteWindowOutlastsProcessDeadline(t *testing.T) {
	srv := (&Server{processDeadline: handlerProcessDeadline, path: defaultPath}).httpServer()

	worstCaseFire := srv.ReadHeaderTimeout + handlerProcessDeadline
	if srv.WriteTimeout <= worstCaseFire {
		t.Errorf("WriteTimeout = %s, but the deadline can fire as late as ReadHeaderTimeout+deadline = %s; "+
			"the connection would be closed before the 503 could be written",
			srv.WriteTimeout, worstCaseFire)
	}
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Error("every listener timeout must stay set; an unset one means an unbounded connection")
	}
}

// TestProcessDeadline_503ReachesARealClient drives an actual TCP listener,
// because that is the only place the bug in #232 is observable: httptest's
// handler-level wiring has no http.Server write deadline, so the 503 always
// appeared to work there while being unreachable in production.
func TestProcessDeadline_503ReachesARealClient(t *testing.T) {
	const deadline = 150 * time.Millisecond

	s := &Server{processDeadline: deadline, path: "/services/collector/event"}
	srv := s.httpServer()
	// Stand in for a handler that outruns the deadline. The receiver's own
	// handler is exercised elsewhere; what is under test here is the interaction
	// between TimeoutHandler and the listener's write deadline.
	srv.Handler = withProcessDeadline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * deadline)
		w.WriteHeader(http.StatusOK)
	}), deadline)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	resp, err := http.Post("http://"+ln.Addr().String()+"/services/collector/event", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("the deadline's 503 never reached the client (connection closed first): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "deadline") {
		t.Errorf("body = %q, want the deadline-exceeded message", body)
	}
}
