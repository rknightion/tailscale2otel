package tsapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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
		onRequest: func(_ context.Context, i RequestInfo) {
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
		onRequest: func(_ context.Context, i RequestInfo) {
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
		onRequest: func(_ context.Context, i RequestInfo) {
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

// retryAfterRoundTripper returns canned statuses; the first (non-final)
// response also carries the given Retry-After header value, so capping tests
// can drive a precise header.
type retryAfterRoundTripper struct {
	statuses   []int
	retryAfter string
	calls      int
}

func (f *retryAfterRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	h := http.Header{}
	if f.calls == 0 && f.retryAfter != "" {
		h.Set("Retry-After", f.retryAfter)
	}
	s := f.statuses[f.calls]
	f.calls++
	return &http.Response{
		StatusCode: s,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// TestRetryTransport_RetryAfterCappedAtMaxDelay covers #206: a Retry-After
// value that exceeds maxDelay must be clamped, not honored verbatim — most
// collectors share an app-lifetime parent context, so an uncapped multi-hour
// Retry-After would park a logical request (and its select on spanCtx.Done())
// far longer than the configured retry maximum intends.
func TestRetryTransport_RetryAfterCappedAtMaxDelay(t *testing.T) {
	t.Run("numeric seconds above cap is clamped to maxDelay", func(t *testing.T) {
		rt := &retryTransport{
			base:      &retryAfterRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}, retryAfter: "3600"},
			max:       2,
			baseDelay: time.Millisecond,
			maxDelay:  15 * time.Millisecond,
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
		elapsed := time.Since(start)
		if elapsed < 10*time.Millisecond {
			t.Fatalf("elapsed = %v, want >= ~maxDelay (Retry-After should still back off up to the cap)", elapsed)
		}
		if elapsed > 500*time.Millisecond {
			t.Fatalf("elapsed = %v, want capped near maxDelay (15ms), not the full 3600s Retry-After", elapsed)
		}
	})

	t.Run("HTTP-date above cap is clamped to maxDelay", func(t *testing.T) {
		future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
		rt := &retryTransport{
			base:      &retryAfterRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}, retryAfter: future},
			max:       2,
			baseDelay: time.Millisecond,
			maxDelay:  15 * time.Millisecond,
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
		elapsed := time.Since(start)
		if elapsed < 10*time.Millisecond {
			t.Fatalf("elapsed = %v, want >= ~maxDelay (Retry-After should still back off up to the cap)", elapsed)
		}
		if elapsed > 500*time.Millisecond {
			t.Fatalf("elapsed = %v, want capped near maxDelay (15ms), not the full 1h Retry-After", elapsed)
		}
	})

	t.Run("numeric seconds below cap is honored exactly", func(t *testing.T) {
		rt := &retryTransport{
			base:      &retryAfterRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}, retryAfter: "1"},
			max:       2,
			baseDelay: time.Millisecond,
			maxDelay:  5 * time.Second,
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
		elapsed := time.Since(start)
		if elapsed < 900*time.Millisecond || elapsed > 1900*time.Millisecond {
			t.Fatalf("elapsed = %v, want ~1s (the sub-cap Retry-After honored exactly)", elapsed)
		}
	})

	t.Run("HTTP-date below cap is honored exactly", func(t *testing.T) {
		// http.TimeFormat has whole-second granularity, so formatting truncates
		// up to ~1s of the offset; add a 2s offset so the parsed-back deadline
		// is always at least ~1s out, and widen the assertion window to match.
		soon := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
		rt := &retryTransport{
			base:      &retryAfterRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}, retryAfter: soon},
			max:       2,
			baseDelay: time.Millisecond,
			maxDelay:  5 * time.Second,
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
		elapsed := time.Since(start)
		if elapsed < 900*time.Millisecond || elapsed > 2900*time.Millisecond {
			t.Fatalf("elapsed = %v, want ~1-2s (the sub-cap Retry-After honored exactly, minus date-format truncation)", elapsed)
		}
	})

	t.Run("zero Retry-After falls through to jittered backoff, unaffected by the cap fix", func(t *testing.T) {
		rt := &retryTransport{
			base:      &retryAfterRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}, retryAfter: "0"},
			max:       2,
			baseDelay: 40 * time.Millisecond,
			maxDelay:  40 * time.Millisecond,
			rnd:       func() float64 { return 0 }, // sleep == baseDelay/2 == 20ms
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
		elapsed := time.Since(start)
		if elapsed < 15*time.Millisecond || elapsed > 500*time.Millisecond {
			t.Fatalf("elapsed = %v, want ~20ms (computeBackoff jitter, Retry-After=0 ignored)", elapsed)
		}
	})
}

// TestRetryTransport_RetryAfterCapDoesNotBlockCancellation verifies that even
// after capping, the wait still selects on the parent (spanCtx) context: a
// cancellation during a capped-but-still-substantial Retry-After wait returns
// immediately rather than waiting out the capped sleep.
func TestRetryTransport_RetryAfterCapDoesNotBlockCancellation(t *testing.T) {
	rt := &retryTransport{
		base:      &retryAfterRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}, retryAfter: "3600"},
		max:       2,
		baseDelay: time.Millisecond,
		maxDelay:  time.Second, // capped sleep would be ~1s; cancellation must beat it
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	start := time.Now()
	_, err := rt.RoundTrip(req)
	if err == nil {
		t.Fatal("err = nil, want context deadline error")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want cancellation to interrupt well before the capped ~1s sleep", elapsed)
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

// A recovered 429 is still evidence that this tailnet reached the provider's
// quota. The final status is 200 after retry, so the signal must be retained
// from the actual intermediate response rather than inferred from final status
// or from client-side queue time.
func TestRequestInfo_RateLimitUtilizationPreservesRecovered429(t *testing.T) {
	var got RequestInfo
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}},
		max:       2,
		baseDelay: time.Millisecond,
		maxDelay:  time.Millisecond,
		rnd:       func() float64 { return 0 },
		onRequest: func(_ context.Context, i RequestInfo) { got = i },
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	defer resp.Body.Close()
	if got.Status != http.StatusOK {
		t.Fatalf("final status = %d, want 200", got.Status)
	}
	if got.RateLimitUtilization != 1 {
		t.Fatalf("RateLimitUtilization = %v, want 1 after a recovered 429", got.RateLimitUtilization)
	}
}

func TestRequestInfo_RateLimitUtilizationIsZeroWithout429(t *testing.T) {
	var got RequestInfo
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusOK}},
		max:       1,
		onRequest: func(_ context.Context, i RequestInfo) { got = i },
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	defer resp.Body.Close()
	if got.RateLimitUtilization != 0 {
		t.Fatalf("RateLimitUtilization = %v, want 0 without a 429", got.RateLimitUtilization)
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

// roundTripFunc is a simple http.RoundTripper backed by a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRoundTrip_EmitsAPISpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var gotSampled bool
	rt := &retryTransport{
		base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
		}),
		max:    1,
		tracer: tp.Tracer("test"),
		onRequest: func(ctx context.Context, _ RequestInfo) {
			gotSampled = trace.SpanContextFromContext(ctx).IsSampled()
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if spans[0].Name() != "tailscale.api devices" {
		t.Errorf("span name = %q, want %q", spans[0].Name(), "tailscale.api devices")
	}
	if !gotSampled {
		t.Error("onRequest ctx must carry the sampled API span (for exemplars)")
	}

	// Guard the attribute keys/values against accidental renames.
	attrs := spanAttrMap(spans[0].Attributes())
	if got := attrs[attribute.Key("http.request.method")].AsString(); got != "GET" {
		t.Errorf("http.request.method = %q, want GET", got)
	}
	if got := attrs[attribute.Key("http.response.status_code")].AsInt64(); got != 200 {
		t.Errorf("http.response.status_code = %d, want 200", got)
	}
	if got := attrs[attribute.Key("http.request.resend_count")].AsInt64(); got != 0 {
		t.Errorf("http.request.resend_count = %d, want 0", got)
	}
	if got := attrs[attribute.Key("url.full")].AsString(); !strings.Contains(got, "/devices") {
		t.Errorf("url.full = %q, want it to contain /devices", got)
	}
}

// spanAttrMap indexes a span's attributes by key for value lookups.
func spanAttrMap(kvs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	m := make(map[attribute.Key]attribute.Value, len(kvs))
	for _, kv := range kvs {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestSanitizeTransportErrorStripsOAuthBody(t *testing.T) {
	re := &oauth2.RetrieveError{
		Response: &http.Response{Status: "500 Internal Server Error", StatusCode: 500},
		Body:     []byte(`{"internal":"SECRET-DETAIL"}`),
	}
	wrapped := &url.Error{Op: "Post", URL: "https://api.tailscale.com/api/v2/oauth/token", Err: re}
	got := sanitizeTransportError(wrapped)
	if strings.Contains(got, "SECRET-DETAIL") {
		t.Errorf("sanitized error still contains the response body: %q", got)
	}
	if !strings.Contains(got, "oauth2") {
		t.Errorf("sanitized error lost the oauth2 context: %q", got)
	}

	// RFC 6749-compliant errors keep their structured code (no body inside).
	re2 := &oauth2.RetrieveError{ErrorCode: "invalid_client"}
	if got := sanitizeTransportError(re2); !strings.Contains(got, "invalid_client") {
		t.Errorf("structured oauth error lost its code: %q", got)
	}

	// Non-OAuth errors pass through untouched.
	plain := errors.New("dial tcp: connection refused")
	if got := sanitizeTransportError(plain); got != plain.Error() {
		t.Errorf("plain error mangled: %q", got)
	}
}

// oauthBodyLeakCases are the response-body shapes #468 names: a token endpoint
// (or a proxy in front of it) can put arbitrary content in the body that
// oauth2.RetrieveError.Error() then embeds verbatim. Each marker must be absent
// from every captured log record at every level.
var oauthBodyLeakCases = map[string]struct{ body, marker string }{
	"json":      {`{"error":"nope","internal":"SECRET-JSON-DETAIL"}`, "SECRET-JSON-DETAIL"},
	"html":      {"<html><body><h1>SECRET-HTML-DETAIL</h1></body></html>", "SECRET-HTML-DETAIL"},
	"bearer":    {"Authorization: Bearer SECRET-BEARER-TOKEN", "SECRET-BEARER-TOKEN"},
	"multiline": {"first line\nSECRET-MULTILINE-DETAIL\nthird line", "SECRET-MULTILINE-DETAIL"},
}

// TestTokenErrorLoggingNeverLeaksResponseBody pins #468: the DEBUG retry path
// logged err.Error() directly, and oauth2.RetrieveError.Error() embeds the RAW
// response body. Both the retry log (DEBUG) and the terminal auth-failure log
// (ERROR) must carry only the bounded diagnostic — class, status, retryability,
// safe message — at every log level, and never the body or the token URL's
// userinfo.
//
// #489: a 401 token error is now terminal, so this case exercises the TERMINAL
// log only — max is still 2, but the loop stops at one attempt. The DEBUG
// retry-record half of #468's proof lives in TestTokenRetryLogNeverLeaksResponseBody,
// which drives a retryable 503.
func TestTokenErrorLoggingNeverLeaksResponseBody(t *testing.T) {
	for name, tc := range oauthBodyLeakCases {
		for _, lvl := range []slog.Level{slog.LevelDebug, slog.LevelInfo} {
			t.Run(name+"/"+lvl.String(), func(t *testing.T) {
				var buf bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: lvl}))
				re := &oauth2.RetrieveError{
					Response: &http.Response{Status: "401 Unauthorized", StatusCode: http.StatusUnauthorized},
					Body:     []byte(tc.body),
				}
				// The URL carries userinfo, which must never reach a log line either.
				wrapped := &url.Error{
					Op:  "Post",
					URL: "https://oauthclient:S3CR3T-CLIENT-SECRET@api.tailscale.com/api/v2/oauth/token",
					Err: re,
				}
				rt := &retryTransport{
					base:      &errRoundTripper{err: wrapped},
					max:       2, // one retry -> exercises logRetry, then logFinal
					baseDelay: time.Millisecond,
					maxDelay:  2 * time.Millisecond,
					rnd:       func() float64 { return 0 },
					logger:    logger,
				}
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
					"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
				if _, err := rt.RoundTrip(req); err == nil {
					t.Fatal("RoundTrip err = nil, want the token error")
				}
				out := buf.String()
				if out == "" {
					t.Fatalf("no log output captured at %v; test proves nothing", lvl)
				}
				if strings.Contains(out, tc.marker) {
					t.Errorf("response body leaked into logs at %v: %s", lvl, out)
				}
				if strings.Contains(out, "S3CR3T-CLIENT-SECRET") {
					t.Errorf("token URL userinfo leaked into logs at %v: %s", lvl, out)
				}
				// Operators must still get a stable class and the status code.
				if !strings.Contains(out, `"error_class":"oauth_error"`) {
					t.Errorf("logs lost the stable diagnostic class at %v: %s", lvl, out)
				}
				if !strings.Contains(out, `"error_status":401`) {
					t.Errorf("logs lost the token-endpoint status at %v: %s", lvl, out)
				}
				// The terminal record carries the attempt bookkeeping and the
				// retryability verdict that now drove the stop (#489).
				for _, key := range []string{`"attempts":1`, `"error_retryable":false`} {
					if !strings.Contains(out, key) {
						t.Errorf("terminal auth-failure log at %v lost %s: %s", lvl, key, out)
					}
				}
			})
		}
	}
}

// TestClassifyTransportError pins the bounded diagnostic vocabulary #468 asks
// for: a stable class, the token-endpoint status, a retryability judgement and
// a message that never carries the raw body.
func TestClassifyTransportError(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantClass    string
		wantStatus   int
		wantRetry    bool
		wantContains string
		wantAbsent   string
	}{
		{
			name:       "oauth RFC6749 code",
			err:        &oauth2.RetrieveError{ErrorCode: "invalid_client", ErrorDescription: "bad client"},
			wantClass:  "oauth_invalid_client",
			wantStatus: 0, wantRetry: false,
			wantContains: "invalid_client",
		},
		{
			name: "oauth raw body 500",
			err: &oauth2.RetrieveError{
				Response: &http.Response{Status: "500 Internal Server Error", StatusCode: 500},
				Body:     []byte(`{"internal":"SECRET-DETAIL"}`),
			},
			wantClass:  "oauth_error",
			wantStatus: 500, wantRetry: true,
			wantContains: "oauth2", wantAbsent: "SECRET-DETAIL",
		},
		{
			name: "oauth unknown code is folded into the generic class",
			err: &oauth2.RetrieveError{
				ErrorCode: "some_vendor_specific_code_" + strings.Repeat("x", 200),
				Response:  &http.Response{Status: "400 Bad Request", StatusCode: 400},
			},
			wantClass:  "oauth_error",
			wantStatus: 400, wantRetry: false,
			wantContains: "oauth2",
		},
		{
			name:      "cross-origin redirect refusal",
			err:       &CrossOriginRedirectError{From: "https://api.tailscale.com:443", To: "https://evil.example:443"},
			wantClass: "redirect_refused",
			wantRetry: false,
			// The refusal is observable via the origins, never the full destination.
			wantContains: "evil.example",
		},
		{
			name:         "plain transport error",
			err:          errors.New("dial tcp: connection refused"),
			wantClass:    "network",
			wantRetry:    true,
			wantContains: "connection refused",
		},
		{
			name:         "context cancellation",
			err:          context.Canceled,
			wantClass:    "canceled",
			wantRetry:    false,
			wantContains: "canceled",
		},
		{
			name:         "context deadline",
			err:          context.DeadlineExceeded,
			wantClass:    "timeout",
			wantRetry:    true,
			wantContains: "deadline",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyTransportError(c.err)
			if got.Class != c.wantClass {
				t.Errorf("Class = %q, want %q", got.Class, c.wantClass)
			}
			if got.Status != c.wantStatus {
				t.Errorf("Status = %d, want %d", got.Status, c.wantStatus)
			}
			if got.Retryable != c.wantRetry {
				t.Errorf("Retryable = %v, want %v", got.Retryable, c.wantRetry)
			}
			if c.wantContains != "" && !strings.Contains(got.Message, c.wantContains) {
				t.Errorf("Message = %q, want it to contain %q", got.Message, c.wantContains)
			}
			if c.wantAbsent != "" && strings.Contains(got.Message, c.wantAbsent) {
				t.Errorf("Message = %q leaks %q", got.Message, c.wantAbsent)
			}
			if strings.ContainsAny(got.Message, "\n\r") {
				t.Errorf("Message is multiline, which breaks single-line log/span fields: %q", got.Message)
			}
		})
	}
}

// TestSanitizeTransportErrorRedactsURLUserinfo covers the #468 requirement that
// the token URL's userinfo never reaches the span status, the admin status page
// or RequestInfo.Err — sanitizeTransportError interpolates url.Error.URL.
func TestSanitizeTransportErrorRedactsURLUserinfo(t *testing.T) {
	wrapped := &url.Error{
		Op:  "Post",
		URL: "https://oauthclient:S3CR3T@api.tailscale.com/api/v2/oauth/token",
		Err: &oauth2.RetrieveError{ErrorCode: "invalid_client"},
	}
	got := sanitizeTransportError(wrapped)
	if strings.Contains(got, "S3CR3T") || strings.Contains(got, "oauthclient") {
		t.Errorf("sanitized error still carries the token URL userinfo: %q", got)
	}
	if !strings.Contains(got, "api.tailscale.com/api/v2/oauth/token") {
		t.Errorf("sanitized error lost the (safe) endpoint identity: %q", got)
	}
	if !strings.Contains(got, "invalid_client") {
		t.Errorf("sanitized error lost the structured oauth code: %q", got)
	}
}

// TestObservedErrorMatchesSanitizedDiagnostic pins that the retry logs, the
// span/status surface (RequestInfo.Err) and the terminal log all render the
// SAME sanitized representation — #468 asks for one representation, not three.
func TestObservedErrorMatchesSanitizedDiagnostic(t *testing.T) {
	re := &oauth2.RetrieveError{
		Response: &http.Response{Status: "500 Internal Server Error", StatusCode: 500},
		Body:     []byte("SECRET-OBSERVE-DETAIL"),
	}
	var got RequestInfo
	rt := &retryTransport{
		base:      &errRoundTripper{err: re},
		max:       1,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(_ context.Context, i RequestInfo) { got = i },
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(got.Err, "SECRET-OBSERVE-DETAIL") {
		t.Errorf("RequestInfo.Err leaks the oauth response body: %q", got.Err)
	}
	if want := classifyTransportError(re).Message; got.Err != want {
		t.Errorf("RequestInfo.Err = %q, want the shared sanitized message %q", got.Err, want)
	}
}

func TestEndpointLabel(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// --- existing stable labels (must not change) ---
		{"/api/v2/tailnet/example.com/devices", "devices"},
		{"/api/v2/tailnet/example.com/users", "users"},
		{"/api/v2/tailnet/example.com/keys", "keys"},
		{"/api/v2/tailnet/example.com/logging/network", "logging/network"},
		{"/api/v2/tailnet/example.com/logging/configuration", "logging/configuration"},
		{"/api/v2/tailnet/example.com/logging/network/stream", "logging/network/stream"},
		{"/api/v2/tailnet/example.com/logging/network/stream/status", "logging/network/stream/status"},
		{"/api/v2/tailnet/example.com/settings", "settings"},
		{"/api/v2/tailnet/example.com/user-invites", "user-invites"},
		{"/api/v2/tailnet/example.com/services", "services"},
		{"/api/v2/tailnet/example.com/posture/integrations", "posture/integrations"},
		{"/api/v2/tailnet/example.com/dns/configuration", "dns/configuration"},
		{"/api/v2/tailnet/example.com/webhooks", "webhooks"},
		{"/api/v2/tailnet/example.com/contacts", "contacts"},
		{"/api/v2/oauth/token", "oauth/token"},
		{"/api/v2/device/dev123/attributes", "device/attributes"},
		{"/api/v2/device/dev123/device-invites", "device/device-invites"},
		// --- elide variable service-name segment (scanner finding 18) ---
		// services/{name}/devices → services/devices
		{"/api/v2/tailnet/example.com/services/svc:argocd/devices", "services/devices"},
		// --- elide variable key-id segment ---
		// keys/{id} → keys
		{"/api/v2/tailnet/example.com/keys/kfoo123", "keys"},
		// --- elide variable user-id segment ---
		// users/{id} → users
		{"/api/v2/tailnet/example.com/users/u987654", "users"},
		// --- elide variable webhook endpoint-id segment (non-tailnet path) ---
		// webhooks/{id} → webhooks
		{"/api/v2/webhooks/ep-abc123", "webhooks"},
		// webhooks/{id}/test → webhooks/test
		{"/api/v2/webhooks/ep-abc123/test", "webhooks/test"},
		// webhooks/{id}/rotate → webhooks/rotate
		{"/api/v2/webhooks/ep-abc123/rotate", "webhooks/rotate"},
		// --- elide variable posture integration-id segment (non-tailnet path) ---
		// posture/integrations/{id} → posture/integrations
		{"/api/v2/posture/integrations/integ-456", "posture/integrations"},
	}
	for _, c := range cases {
		if got := endpointLabel(c.path); got != c.want {
			t.Errorf("endpointLabel(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
