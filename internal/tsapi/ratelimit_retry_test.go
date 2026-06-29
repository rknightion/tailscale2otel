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
