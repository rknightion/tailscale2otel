package tsapi

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// countingWaiter records how many times Wait is invoked, never blocking.
type countingWaiter struct{ n int }

func (c *countingWaiter) Wait(context.Context) error { c.n++; return nil }

// TestRetryAcquiresTokenPerAttempt verifies that the retry transport's limiter
// is consulted on every attempt — including retries — not just the first.
func TestRetryAcquiresTokenPerAttempt(t *testing.T) {
	cw := &countingWaiter{}
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}},
		limiter:   cw,
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if cw.n != 2 {
		t.Fatalf("Wait called %d times, want 2 (one per attempt incl. the retry)", cw.n)
	}
}

// blockingWaiter blocks for d (or until ctx is done) on each Wait, simulating a
// long queue wait for a rate-limiter token.
type blockingWaiter struct{ d time.Duration }

func (b blockingWaiter) Wait(ctx context.Context) error {
	select {
	case <-time.After(b.d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestLimiterWaitNotChargedToAttemptTimeout proves that a long rate-limiter wait
// is NOT bounded by the per-attempt HTTP timeout: the limiter wait (40ms) far
// exceeds attemptTimeout (10ms), yet the request succeeds because the wait runs
// on the parent context before the attempt-timeout context is applied. Were the
// limiter still the retry transport's base (inside attemptTimeout), the wait
// would be canceled and the request would fail as a deadline error.
func TestLimiterWaitNotChargedToAttemptTimeout(t *testing.T) {
	rt := &retryTransport{
		base:           &fakeRoundTripper{statuses: []int{http.StatusOK}},
		limiter:        blockingWaiter{d: 40 * time.Millisecond},
		attemptTimeout: 10 * time.Millisecond, // shorter than the limiter wait
		max:            1,
		baseDelay:      time.Millisecond,
		maxDelay:       2 * time.Millisecond,
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v (a long limiter wait must not fail as an attempt timeout)", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestRequestInfo_WaitDurationReflectsLimiterWaitAndIsExcludedFromDuration
// (#76) proves that RequestInfo separates the client-side rate-limiter queue
// wait from the round-trip/backoff latency: WaitDuration reflects the actual
// time blocked on the limiter, and Duration (what api.duration is built from)
// does NOT include it — the round trip itself is near-instant here, so a
// Duration anywhere close to the 40ms limiter wait would mean the wait leaked
// back into it.
func TestRequestInfo_WaitDurationReflectsLimiterWaitAndIsExcludedFromDuration(t *testing.T) {
	var got RequestInfo
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusOK}},
		limiter:   blockingWaiter{d: 40 * time.Millisecond},
		max:       1,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(_ context.Context, i RequestInfo) {
			got = i
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got.WaitDuration < 35*time.Millisecond {
		t.Fatalf("WaitDuration = %v, want >= ~40ms (the limiter wait)", got.WaitDuration)
	}
	if got.Duration >= 20*time.Millisecond {
		t.Fatalf("Duration = %v, want well under the 40ms limiter wait (it must be excluded)", got.Duration)
	}
}

// TestRequestInfo_WaitDurationAccumulatesAcrossRetries verifies the limiter
// wait is summed across every attempt of a retried logical request, not just
// the first.
func TestRequestInfo_WaitDurationAccumulatesAcrossRetries(t *testing.T) {
	var got RequestInfo
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}},
		limiter:   blockingWaiter{d: 15 * time.Millisecond},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(_ context.Context, i RequestInfo) {
			got = i
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got.Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2", got.Attempts)
	}
	// Two attempts, each waiting ~15ms on the limiter -> >= ~30ms total.
	if got.WaitDuration < 28*time.Millisecond {
		t.Fatalf("WaitDuration = %v, want >= ~30ms (2 attempts x 15ms)", got.WaitDuration)
	}
}

// TestRequestInfo_WaitDurationZeroWithoutLimiter verifies a nil limiter (the
// unlimited/pass-through configuration) reports a zero WaitDuration rather than
// leaving it uninitialized-but-wrong.
func TestRequestInfo_WaitDurationZeroWithoutLimiter(t *testing.T) {
	var got RequestInfo
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusOK}},
		max:       1,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(_ context.Context, i RequestInfo) {
			got = i
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got.WaitDuration != 0 {
		t.Fatalf("WaitDuration = %v, want 0 (no limiter configured)", got.WaitDuration)
	}
}
