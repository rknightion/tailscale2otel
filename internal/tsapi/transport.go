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

// RequestInfo describes one completed logical API request (after all retries),
// reported to Options.OnRequest. Err is the transport error string ("" on an
// HTTP response of any status); it never contains response body or header data.
type RequestInfo struct {
	Endpoint string        // low-cardinality label (endpointLabel)
	Status   int           // final HTTP status, 0 on transport error
	Attempts int           // total attempts incl. the first
	Duration time.Duration // wall-clock of the whole logical request (incl. retries/backoff)
	Err      string        // transport error text, "" when an HTTP response was received
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
	// of each logical request with a RequestInfo describing the outcome.
	onRequest func(RequestInfo)
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	var (
		resp  *http.Response
		err   error
		delay = t.baseDelay
	)
	for attempt := 1; ; attempt++ {
		attemptReq := req.Clone(req.Context())
		// Clone shares the original Body reader, which the previous attempt would
		// have drained; rewind it from GetBody so a retried request with a body
		// (e.g. a PUT) re-sends its payload. GetBody is nil for bodyless GETs.
		if req.GetBody != nil {
			body, gbErr := req.GetBody()
			if gbErr != nil {
				t.observe(req, nil, gbErr, attempt, start)
				return nil, gbErr
			}
			attemptReq.Body = body
		}
		resp, err = t.base.RoundTrip(attemptReq)
		if err == nil && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			t.observe(req, resp, err, attempt, start)
			return resp, nil
		}
		if attempt >= t.max {
			t.observe(req, resp, err, attempt, start)
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
			t.observe(req, nil, req.Context().Err(), attempt, start)
			return nil, req.Context().Err()
		case <-time.After(sleep):
		}
		if delay *= 2; delay > t.maxDelay {
			delay = t.maxDelay
		}
	}
}

// observe reports a completed logical request to the configured hook, if any.
// start is the monotonic time the logical request began (captured in RoundTrip),
// used to compute the wall-clock duration across all retries and backoff.
func (t *retryTransport) observe(req *http.Request, resp *http.Response, err error, attempts int, start time.Time) {
	if t.onRequest == nil {
		return
	}
	status := 0
	if err == nil && resp != nil {
		status = resp.StatusCode
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	t.onRequest(RequestInfo{
		Endpoint: endpointLabel(req.URL.Path),
		Status:   status,
		Attempts: attempts,
		Duration: time.Since(start),
		Err:      errStr,
	})
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
