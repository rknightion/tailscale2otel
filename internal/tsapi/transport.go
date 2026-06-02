package tsapi

import (
	"io"
	"net/http"
	"strconv"
	"strings"
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

	// onRequest, when non-nil, is called exactly once after the final attempt
	// of each logical request with a low-cardinality endpoint label, the final
	// HTTP status (0 on transport error) and the total attempt count.
	onRequest func(endpoint string, status, attempts int)
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
			t.observe(req, resp, err, attempt)
			return resp, nil
		}
		if attempt >= t.max {
			t.observe(req, resp, err, attempt)
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
			t.observe(req, nil, req.Context().Err(), attempt)
			return nil, req.Context().Err()
		case <-time.After(sleep):
		}
		if delay *= 2; delay > t.maxDelay {
			delay = t.maxDelay
		}
	}
}

// observe reports a completed logical request to the configured hook, if any.
func (t *retryTransport) observe(req *http.Request, resp *http.Response, err error, attempts int) {
	if t.onRequest == nil {
		return
	}
	status := 0
	if err == nil && resp != nil {
		status = resp.StatusCode
	}
	t.onRequest(endpointLabel(req.URL.Path), status, attempts)
}

// endpointLabel derives a stable, low-cardinality label from an API request
// path by stripping the "/api/v2/tailnet/{tailnet}/" prefix, e.g. "devices",
// "logging/network" or "user-invites". Non-tailnet paths get a short stable
// label: "oauth/token" or, for per-device endpoints, "device/{leaf}" with the
// variable device id elided.
func endpointLabel(p string) string {
	p = strings.Trim(p, "/")
	p = strings.TrimPrefix(p, "api/v2/")
	if rest, ok := strings.CutPrefix(p, "tailnet/"); ok {
		// Drop the tailnet segment.
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return rest[i+1:]
		}
		return rest
	}
	if rest, ok := strings.CutPrefix(p, "device/"); ok {
		// device/{id}/attributes -> device/attributes (elide variable id).
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return "device/" + rest[i+1:]
		}
		return "device"
	}
	return p
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
