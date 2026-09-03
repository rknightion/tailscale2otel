package tsapi

import (
	"net/http"

	"github.com/rknightion/tailscale2otel/v5/internal/httpretry"
)

// Compatibility aliases keep retryTransport and its unchanged tests on their
// existing private seam while the provider-neutral implementation lives in
// internal/httpretry.
type limiter = httpretry.Limiter
type rateWaiter = httpretry.Waiter

func newLimiter(ratePerSec float64) *limiter {
	w := httpretry.NewWaiter(ratePerSec)
	if w == nil {
		return nil
	}
	return w.(*limiter)
}

func wrapRateLimit(base http.RoundTripper, ratePerSec float64) http.RoundTripper {
	return httpretry.WrapRateLimit(base, ratePerSec)
}

func newRateWaiter(ratePerSec float64) rateWaiter { return httpretry.NewWaiter(ratePerSec) }
