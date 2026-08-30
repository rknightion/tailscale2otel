// Package httpretry provides provider-neutral HTTP retry and rate-limit primitives.
package httpretry

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limiter is a small thread-safe token-bucket rate limiter. Tokens refill
// continuously at ratePerSec and the bucket holds at most burst tokens.
type Limiter struct {
	mu         sync.Mutex
	ratePerSec float64
	burst      float64
	tokens     float64
	last       time.Time
}

// NewWaiter returns a token-bucket waiter at ratePerSec requests per second,
// or nil when ratePerSec <= 0 (unlimited).
func NewWaiter(ratePerSec float64) Waiter {
	if ratePerSec <= 0 {
		return nil
	}
	return &Limiter{ratePerSec: ratePerSec, burst: 1, tokens: 1, last: time.Now()}
}

// reserve refills the bucket for elapsed time and, if a token is available,
// consumes it and returns 0. Otherwise it returns the duration to wait until
// the next token will be available (without consuming one).
func (l *Limiter) reserve(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if elapsed := now.Sub(l.last); elapsed > 0 {
		l.tokens += elapsed.Seconds() * l.ratePerSec
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return 0
	}
	need := 1 - l.tokens
	return time.Duration(need / l.ratePerSec * float64(time.Second))
}

// Wait blocks until a token is available or ctx is done, in which case it
// returns ctx.Err().
func (l *Limiter) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait := l.reserve(time.Now())
		if wait <= 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Re-check: another goroutine may have taken the token first.
		}
	}
}

// Waiter blocks until a request may proceed (or ctx is done). *Limiter is the
// production implementation; tests substitute a fake.
type Waiter interface {
	Wait(ctx context.Context) error
}

// RateLimitTransport waits for a limiter token before each round-trip.
type RateLimitTransport struct {
	base http.RoundTripper
	lim  Waiter
}

// RoundTrip implements http.RoundTripper.
func (t *RateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.lim.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// WrapRateLimit wraps base in a rate-limiting transport at ratePerSec requests
// per second. When ratePerSec <= 0 it returns base unchanged (pass-through).
func WrapRateLimit(base http.RoundTripper, ratePerSec float64) http.RoundTripper {
	if lim := NewWaiter(ratePerSec); lim != nil {
		return &RateLimitTransport{base: base, lim: lim}
	}
	return base
}

// ComputeBackoff returns the equal-jittered sleep for the current delay and
// the next (doubled, capped) base delay. rnd must be in [0,1).
func ComputeBackoff(delay, maxDelay time.Duration, rnd float64) (sleep, next time.Duration) {
	half := delay / 2
	sleep = half + time.Duration(rnd*float64(half))
	next = min(delay*2, maxDelay)
	return sleep, next
}

// RetryAfter parses a Retry-After header as either seconds or an HTTP date.
func RetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(h); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// RetryableOutcome reports whether a generic HTTP outcome warrants a retry.
// A caller with provider-specific transport-error classification should handle
// err itself and delegate only response statuses here.
func RetryableOutcome(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	return resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500)
}
