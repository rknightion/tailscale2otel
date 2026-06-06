package tsapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeRoundTripper returns canned responses in order, one per call.
type fakeRoundTripper struct {
	statuses []int
	calls    int
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s := f.statuses[f.calls]
	f.calls++
	return &http.Response{
		StatusCode: s,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// errRoundTripper always fails with a transport error, exercising the err!=0,
// status==0 path of observe.
type errRoundTripper struct {
	err   error
	calls int
}

func (f *errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	return nil, f.err
}

func TestRetryTransport_ObserverSeesFinalStatusAndAttempts(t *testing.T) {
	var (
		got   RequestInfo
		calls int
	)
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(i RequestInfo) {
			calls++
			got = i
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices?fields=all", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("observer called %d times, want exactly 1", calls)
	}
	if got.Status != http.StatusOK {
		t.Fatalf("observed status = %d, want 200", got.Status)
	}
	if got.Attempts != 2 {
		t.Fatalf("observed attempts = %d, want 2", got.Attempts)
	}
	if got.Endpoint != "devices" {
		t.Fatalf("observed endpoint = %q, want %q", got.Endpoint, "devices")
	}
	if got.Duration <= 0 {
		t.Fatalf("observed duration = %v, want > 0", got.Duration)
	}
	if got.Err != "" {
		t.Fatalf("observed err = %q, want empty", got.Err)
	}
}

func TestRetryTransport_ObserverFirstTry(t *testing.T) {
	var got RequestInfo
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusOK}},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(i RequestInfo) {
			got = i
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/logging/network?start=x", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", got.Attempts)
	}
	if got.Status != http.StatusOK {
		t.Fatalf("status = %d", got.Status)
	}
	if got.Endpoint != "logging/network" {
		t.Fatalf("endpoint = %q, want logging/network", got.Endpoint)
	}
	if got.Duration <= 0 {
		t.Fatalf("duration = %v, want > 0", got.Duration)
	}
	if got.Err != "" {
		t.Fatalf("err = %q, want empty", got.Err)
	}
}

// TestRetryTransport_ObserverTransportError verifies that when every attempt
// fails at the transport layer (no HTTP response), the observer sees Status==0
// and a non-empty Err carrying the transport error text.
func TestRetryTransport_ObserverTransportError(t *testing.T) {
	var got RequestInfo
	base := &errRoundTripper{err: errors.New("dial tcp: connection refused")}
	rt := &retryTransport{
		base:      base,
		max:       2,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(i RequestInfo) {
			got = i
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatalf("RoundTrip err = nil, want transport error")
	}
	if got.Status != 0 {
		t.Fatalf("status = %d, want 0 on transport error", got.Status)
	}
	if got.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", got.Attempts)
	}
	if got.Endpoint != "devices" {
		t.Fatalf("endpoint = %q, want devices", got.Endpoint)
	}
	if got.Err == "" {
		t.Fatalf("err = empty, want transport error text")
	}
	if got.Duration <= 0 {
		t.Fatalf("duration = %v, want > 0", got.Duration)
	}
}

func TestRetryTransport_NilObserverNoPanic(t *testing.T) {
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusOK}},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/keys", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// bodyCapturingRoundTripper records each attempt's request body and returns
// canned statuses, so a retried request's re-sent body can be asserted.
type bodyCapturingRoundTripper struct {
	statuses []int
	bodies   []string
	calls    int
}

func (f *bodyCapturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	f.bodies = append(f.bodies, body)
	s := f.statuses[f.calls]
	f.calls++
	return &http.Response{
		StatusCode: s,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// TestRetryTransport_ResendsBodyOnRetry verifies that a request carrying a body
// (e.g. the log-stream-config PUT) re-sends its full payload on a retried
// attempt instead of an empty body: the retry loop must rewind Body from
// GetBody each attempt. Without that, the second attempt would see "".
func TestRetryTransport_ResendsBodyOnRetry(t *testing.T) {
	capt := &bodyCapturingRoundTripper{statuses: []int{http.StatusInternalServerError, http.StatusOK}}
	rt := &retryTransport{
		base:      capt,
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
	}
	const payload = `{"destinationType":"splunk","url":"https://x"}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		"https://api.tailscale.com/api/v2/tailnet/example.com/logging/network/stream",
		strings.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if capt.calls != 2 {
		t.Fatalf("calls = %d, want 2 (one retry)", capt.calls)
	}
	for i, b := range capt.bodies {
		if b != payload {
			t.Fatalf("attempt %d body = %q, want full payload re-sent", i+1, b)
		}
	}
}

func TestComputeBackoff(t *testing.T) {
	maxDelay := 10 * time.Second
	// rnd=0 -> sleep == delay/2 (lower bound); next == delay*2 capped.
	sleep, next := computeBackoff(1*time.Second, maxDelay, 0)
	if sleep != 500*time.Millisecond {
		t.Fatalf("rnd=0 sleep = %v, want 500ms", sleep)
	}
	if next != 2*time.Second {
		t.Fatalf("next = %v, want 2s", next)
	}
	// rnd just below 1 -> sleep approaches delay (upper bound, exclusive).
	sleep, _ = computeBackoff(1*time.Second, maxDelay, 0.999)
	if sleep < 500*time.Millisecond || sleep >= 1*time.Second {
		t.Fatalf("rnd~1 sleep = %v, want in [500ms, 1s)", sleep)
	}
	// next is capped at maxDelay.
	_, next = computeBackoff(8*time.Second, maxDelay, 0)
	if next != maxDelay {
		t.Fatalf("capped next = %v, want %v", next, maxDelay)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		in    string
		want  time.Duration // used when exact; -1 means "small positive"
		exact bool
	}{
		{"empty", "", 0, true},
		{"seconds", "5", 5 * time.Second, true},
		{"zero seconds", "0", 0, true},
		{"negative seconds", "-3", 0, true},
		{"garbage", "soon", 0, true},
		{"http date future", now.Add(7 * time.Second).UTC().Format(http.TimeFormat), -1, false},
		{"http date past", now.Add(-7 * time.Second).UTC().Format(http.TimeFormat), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := retryAfter(c.in)
			switch {
			case c.exact:
				if got != c.want {
					t.Fatalf("retryAfter(%q) = %v, want %v", c.in, got, c.want)
				}
			case c.want == -1:
				if got <= 0 || got > 8*time.Second {
					t.Fatalf("retryAfter(%q) = %v, want a small positive duration", c.in, got)
				}
			default: // past date -> 0 (clamped)
				if got != 0 {
					t.Fatalf("retryAfter(%q) = %v, want 0", c.in, got)
				}
			}
		})
	}
}

func TestCancelOnCloseBody(t *testing.T) {
	called := false
	b := &cancelOnCloseBody{ReadCloser: io.NopCloser(strings.NewReader("hi")), cancel: func() { called = true }}
	got, _ := io.ReadAll(b)
	if string(got) != "hi" {
		t.Fatalf("read = %q, want hi", got)
	}
	if called {
		t.Fatal("cancel called before Close")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !called {
		t.Fatal("cancel not called on Close")
	}
}

// blockingRoundTripper blocks until the request context is done, then returns
// its error — modeling a hung attempt.
type blockingRoundTripper struct{ calls int }

func (b *blockingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	b.calls++
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestRetryTransport_PerAttemptTimeoutBoundsHungAttempt(t *testing.T) {
	rt := &retryTransport{
		base:           &blockingRoundTripper{},
		max:            2,
		baseDelay:      time.Millisecond,
		maxDelay:       2 * time.Millisecond,
		attemptTimeout: 20 * time.Millisecond,
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	start := time.Now()
	_, err := rt.RoundTrip(req)
	if err == nil {
		t.Fatal("err = nil, want a deadline error")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want bounded by per-attempt timeout", elapsed)
	}
}

func TestRetryTransport_BackoffNotClippedByAttemptTimeout(t *testing.T) {
	// attemptTimeout is tiny, but the backoff sleep waits on the parent context,
	// so the full (jittered) sleep must elapse between the 429 and the retry.
	rt := &retryTransport{
		base:           &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}},
		max:            2,
		baseDelay:      60 * time.Millisecond,
		maxDelay:       60 * time.Millisecond,
		attemptTimeout: 5 * time.Millisecond,
		rnd:            func() float64 { return 0 }, // sleep == delay/2 == 30ms
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	start := time.Now()
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= ~30ms (backoff not clipped to attemptTimeout)", elapsed)
	}
}

func TestRetryTransport_WrapsBodyForCancel(t *testing.T) {
	rt := &retryTransport{
		base:           &fakeRoundTripper{statuses: []int{http.StatusOK}},
		max:            2,
		baseDelay:      time.Millisecond,
		maxDelay:       2 * time.Millisecond,
		attemptTimeout: time.Second,
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if _, ok := resp.Body.(*cancelOnCloseBody); !ok {
		t.Fatalf("resp.Body type = %T, want *cancelOnCloseBody", resp.Body)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// recordingHandler captures slog records for assertions.
type recordingHandler struct{ records []slog.Record }

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func levelOf(t *testing.T, h *recordingHandler, wantMsgSub string) slog.Level {
	t.Helper()
	for _, r := range h.records {
		if strings.Contains(r.Message, wantMsgSub) {
			return r.Level
		}
	}
	t.Fatalf("no log record containing %q (have %d records)", wantMsgSub, len(h.records))
	return 0
}

func runWithLog(t *testing.T, statuses []int) *recordingHandler {
	t.Helper()
	h := &recordingHandler{}
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: statuses},
		max:       len(statuses),
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		rnd:       func() float64 { return 0 },
		logger:    slog.New(h),
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	return h
}

func TestTransportLogging(t *testing.T) {
	t.Run("429 backoff logs INFO", func(t *testing.T) {
		h := runWithLog(t, []int{http.StatusTooManyRequests, http.StatusOK})
		if lvl := levelOf(t, h, "rate limited"); lvl != slog.LevelInfo {
			t.Fatalf("429 backoff level = %v, want INFO", lvl)
		}
	})
	t.Run("5xx backoff logs DEBUG", func(t *testing.T) {
		h := runWithLog(t, []int{http.StatusInternalServerError, http.StatusOK})
		if lvl := levelOf(t, h, "retrying"); lvl != slog.LevelDebug {
			t.Fatalf("5xx backoff level = %v, want DEBUG", lvl)
		}
	})
	t.Run("final 401 logs ERROR", func(t *testing.T) {
		h := runWithLog(t, []int{http.StatusUnauthorized})
		if lvl := levelOf(t, h, "unauthorized"); lvl != slog.LevelError {
			t.Fatalf("401 level = %v, want ERROR", lvl)
		}
	})
	t.Run("final 403 logs nothing", func(t *testing.T) {
		h := runWithLog(t, []int{http.StatusForbidden})
		if len(h.records) != 0 {
			t.Fatalf("403 produced %d log records, want 0", len(h.records))
		}
	})
	t.Run("oauth retrieve 401 logs ERROR", func(t *testing.T) {
		h := &recordingHandler{}
		rt := &retryTransport{
			base:      &errRoundTripper{err: &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}}},
			max:       1,
			baseDelay: time.Millisecond,
			maxDelay:  2 * time.Millisecond,
			logger:    slog.New(h),
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
			"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
		if _, err := rt.RoundTrip(req); err == nil {
			t.Fatal("want error")
		}
		if lvl := levelOf(t, h, "OAuth token request failed"); lvl != slog.LevelError {
			t.Fatalf("oauth 401 level = %v, want ERROR", lvl)
		}
	})
	t.Run("nil logger does not panic", func(t *testing.T) {
		rt := &retryTransport{
			base:      &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}},
			max:       2,
			baseDelay: time.Millisecond,
			maxDelay:  2 * time.Millisecond,
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
			"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
		if _, err := rt.RoundTrip(req); err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
	})
}

func TestEndpointLabel(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/v2/tailnet/example.com/devices", "devices"},
		{"/api/v2/tailnet/example.com/users", "users"},
		{"/api/v2/tailnet/example.com/keys", "keys"},
		{"/api/v2/tailnet/example.com/logging/network", "logging/network"},
		{"/api/v2/tailnet/example.com/logging/configuration", "logging/configuration"},
		{"/api/v2/tailnet/example.com/settings", "settings"},
		{"/api/v2/tailnet/example.com/user-invites", "user-invites"},
		{"/api/v2/oauth/token", "oauth/token"},
		{"/api/v2/device/dev123/attributes", "device/attributes"},
	}
	for _, c := range cases {
		if got := endpointLabel(c.path); got != c.want {
			t.Errorf("endpointLabel(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
