package stream

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
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

// TestWithProcessDeadline_DerivesDeadlineContext exercises the wrapper's
// millisecond deadline without relying on a sleeping handler. The handler owns
// the response, which is what keeps a deadline response behind in-flight work.
func TestWithProcessDeadline_DerivesDeadlineContext(t *testing.T) {
	deadlineSeen := make(chan struct{})
	h := withProcessDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("request context has no processing deadline")
		}
		close(deadlineSeen)
		<-r.Context().Done()
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"text":"request processing deadline exceeded","code":503}`)
	}), time.Millisecond)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/services/collector/event", nil))
	<-deadlineSeen

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 once the deadline elapses", w.Code)
	}
	if !strings.Contains(w.Body.String(), "deadline") {
		t.Fatalf("body = %q, want it to mention the deadline", w.Body.String())
	}
}

// TestHandler_ProcessDeadlineWaitsForInFlightCallback pins #278's response
// boundary: a deadline prevents Phase 2 from starting new effects, but it never
// sends a 503 while an already-started effect is still running. The raw-body
// ingest callback is deliberately the first Phase-2 action; keeping it blocked
// proves that the flow processor and the success ACK remain behind the same
// cancellation boundary.
func TestHandler_ProcessDeadlineWaitsForInFlightCallback(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	callbackDone := make(chan struct{})
	rec := telemetrytest.New()
	s := New(Options{
		Listen: "127.0.0.1:0",
		OnIngest: func(_, _ string, _, _ int) {
			close(entered)
			<-unblock
			close(callbackDone)
		},
	}, flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}), audit.NewProcessor(), rec.Emitter(), nil)
	s.processDeadline = 5 * time.Millisecond

	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, defaultPath,
			strings.NewReader(`{"nodeId":"n1","virtualTraffic":[{"proto":6,"src":"100.64.0.1:443","dst":"100.64.0.2:51820","txBytes":1}]}`))
		req.Host = "127.0.0.1:9099"
		s.Handler().ServeHTTP(w, req)
		response <- w
	}()

	<-entered
	deadline, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	select {
	case w := <-response:
		close(unblock)
		<-callbackDone
		t.Fatalf("response arrived with status %d while the in-flight callback was still blocked", w.Code)
	case <-deadline.Done():
	}
	if logs := rec.LogRecords(); len(logs) != 0 {
		close(unblock)
		<-callbackDone
		t.Fatalf("flow processor ran after cancellation while callback was blocked: %d logs", len(logs))
	}

	close(unblock)
	<-callbackDone
	w := <-response
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 after the in-flight callback returned", w.Code)
	}
	if logs := rec.LogRecords(); len(logs) != 0 {
		t.Fatalf("flow processor ran after the deadline: %d logs", len(logs))
	}
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
// window is still open when the handler observes its context deadline.
//
// The margin needed is NOT just handlerProcessDeadline. http.Server starts the
// write deadline when the request's headers begin arriving, but the handler's
// processing timer starts when ServeHTTP is entered — and those are separated by up to
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
	// Stand in for the receiver after it observes its context deadline. The
	// receiver's Phase-2 boundary is exercised elsewhere; what is under test here
	// is the listener write deadline that must leave this 503 deliverable.
	srv.Handler = withProcessDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"text":"request processing deadline exceeded","code":503}`)
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
