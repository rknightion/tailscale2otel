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

// TestRetryAcquiresTokenPerAttempt verifies that when the rate limiter is the
// retry transport's base (inner), every attempt — including retries — acquires
// a token, not just the first.
func TestRetryAcquiresTokenPerAttempt(t *testing.T) {
	cw := &countingWaiter{}
	rt := &retryTransport{
		base:      &rateLimitTransport{base: &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}}, lim: cw},
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
