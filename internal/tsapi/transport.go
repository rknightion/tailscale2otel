package tsapi

import (
	"io"
	"net/http"
	"strconv"
	"time"
)

// authKeyTransport injects a Bearer token on each request.
type authKeyTransport struct {
	base http.RoundTripper
	key  string
}

func (t *authKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+t.key)
	return t.base.RoundTrip(r)
}

// retryTransport retries 429 and 5xx responses (and transport errors) with
// exponential backoff, honoring Retry-After. Safe for the idempotent, bodyless
// GETs used by this package.
type retryTransport struct {
	base      http.RoundTripper
	max       int
	baseDelay time.Duration
	maxDelay  time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var (
		resp  *http.Response
		err   error
		delay = t.baseDelay
	)
	for attempt := 1; ; attempt++ {
		resp, err = t.base.RoundTrip(req.Clone(req.Context()))
		if err == nil && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		}
		if attempt >= t.max {
			return resp, err
		}
		sleep := delay
		if resp != nil {
			if ra := retryAfter(resp.Header.Get("Retry-After")); ra > 0 {
				sleep = ra
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(sleep):
		}
		if delay *= 2; delay > t.maxDelay {
			delay = t.maxDelay
		}
	}
}

func retryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}
