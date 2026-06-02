package tsapi

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// limiter is a small thread-safe token-bucket rate limiter. Tokens refill
// continuously at ratePerSec and the bucket holds at most burst tokens.
type limiter struct {
	mu         sync.Mutex
	ratePerSec float64
	burst      float64
	tokens     float64
	last       time.Time
}

// newLimiter returns a limiter allowing ratePerSec requests per second with a
// burst capacity of one token (the bucket starts full).
func newLimiter(ratePerSec float64) *limiter {
	return &limiter{
		ratePerSec: ratePerSec,
		burst:      1,
		tokens:     1,
		last:       time.Now(),
	}
}

// reserve refills the bucket for elapsed time and, if a token is available,
// consumes it and returns 0. Otherwise it returns the duration to wait until
// the next token will be available (without consuming one).
func (l *limiter) reserve(now time.Time) time.Duration {
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
func (l *limiter) Wait(ctx context.Context) error {
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

// rateLimitTransport waits for a limiter token before each round-trip. It is the
// outermost transport so every logical request is rate limited.
type rateLimitTransport struct {
	base http.RoundTripper
	lim  *limiter
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.lim.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// wrapRateLimit wraps base in a rate-limiting transport at ratePerSec requests
// per second. When ratePerSec <= 0 it returns base unchanged (pass-through).
func wrapRateLimit(base http.RoundTripper, ratePerSec float64) http.RoundTripper {
	if ratePerSec <= 0 {
		return base
	}
	return &rateLimitTransport{base: base, lim: newLimiter(ratePerSec)}
}
