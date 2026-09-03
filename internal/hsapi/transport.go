package hsapi

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/httpretry"
)

type retryStateKey struct{}

type retryState struct {
	attempts     int
	waitDuration time.Duration
}

// retryTransport retries transient, idempotent Headscale GET outcomes. The
// limiter waits on the parent request context before the per-attempt timeout,
// so client-side queueing is never charged against HTTP I/O time.
type retryTransport struct {
	base           http.RoundTripper
	max            int
	baseDelay      time.Duration
	maxDelay       time.Duration
	attemptTimeout time.Duration
	limiter        httpretry.Waiter
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	state, _ := req.Context().Value(retryStateKey{}).(*retryState)
	setState := func(attempts int, wait time.Duration) {
		if state != nil {
			state.attempts = attempts
			state.waitDuration = wait
		}
	}

	delay := t.baseDelay
	var waitTotal time.Duration
	for attempt := 1; ; attempt++ {
		if t.limiter != nil {
			waitStart := time.Now()
			err := t.limiter.Wait(req.Context())
			waitTotal += time.Since(waitStart)
			if err != nil {
				setState(attempt, waitTotal)
				return nil, err
			}
		}

		attemptCtx := req.Context()
		var cancel context.CancelFunc
		if t.attemptTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(req.Context(), t.attemptTimeout)
		}
		attemptReq := req.Clone(attemptCtx)
		resp, err := t.base.RoundTrip(attemptReq)
		if !httpretry.RetryableOutcome(resp, err) || attempt >= t.max {
			if cancel != nil {
				if resp != nil && resp.Body != nil {
					resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
				} else {
					cancel()
				}
			}
			setState(attempt, waitTotal)
			return resp, err
		}

		sleep, next := httpretry.ComputeBackoff(delay, t.maxDelay, rand.Float64()) //nolint:gosec // backoff jitter is not security-sensitive
		if resp != nil {
			if retryAfter := httpretry.RetryAfter(resp.Header.Get("Retry-After")); retryAfter > 0 {
				sleep = min(retryAfter, t.maxDelay)
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
			_ = resp.Body.Close()
		}
		if cancel != nil {
			cancel()
		}
		select {
		case <-req.Context().Done():
			setState(attempt, waitTotal)
			return nil, req.Context().Err()
		case <-time.After(sleep):
		}
		delay = next
	}
}

// cancelOnCloseBody keeps a successful final attempt's timeout active while
// the caller reads its response body, matching net/http.Client.Timeout.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
