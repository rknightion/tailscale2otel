package tsapi

import (
	"context"
	"io"
	"math/rand/v2"
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

	// rnd, when non-nil, supplies the jitter fraction in [0,1) (tests inject a
	// fixed value); nil uses math/rand.
	rnd func() float64

	// attemptTimeout bounds each individual HTTP attempt (connect + headers +
	// body read), not the whole retried request. Zero disables it. Backoff
	// sleeps are NOT bounded by it — they wait on the parent request context, so
	// a long Retry-After is honored.
	attemptTimeout time.Duration

	// onRequest, when non-nil, is called exactly once after the final attempt
	// of each logical request with a RequestInfo describing the outcome.
	onRequest func(RequestInfo)
}

// cancelOnCloseBody ties a per-attempt context's cancel to the lifetime of the
// response body: the deadline keeps covering body reads, and the context is
// released when the caller closes the body. This is the same pattern the stdlib
// http.Client uses for its own Timeout.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// logFinal and logRetry are filled in by Task 5 (status-aware logging); no-ops
// for now so the per-attempt-timeout restructure compiles independently.
func (t *retryTransport) logFinal(*http.Request, *http.Response, error, int)               {}
func (t *retryTransport) logRetry(*http.Request, *http.Response, error, int, time.Duration) {}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	var (
		resp  *http.Response
		err   error
		delay = t.baseDelay
	)
	for attempt := 1; ; attempt++ {
		actx := req.Context()
		var cancel context.CancelFunc
		if t.attemptTimeout > 0 {
			actx, cancel = context.WithTimeout(req.Context(), t.attemptTimeout)
		}
		attemptReq := req.Clone(actx)
		// Clone shares the original Body reader, which the previous attempt would
		// have drained; rewind it from GetBody so a retried request with a body
		// (e.g. a PUT) re-sends its payload. GetBody is nil for bodyless GETs.
		if req.GetBody != nil {
			body, gbErr := req.GetBody()
			if gbErr != nil {
				if cancel != nil {
					cancel()
				}
				t.observe(req, nil, gbErr, attempt, start)
				return nil, gbErr
			}
			attemptReq.Body = body
		}
		resp, err = t.base.RoundTrip(attemptReq)
		retryable := err != nil || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if !retryable || attempt >= t.max {
			t.logFinal(req, resp, err, attempt)
			if cancel != nil {
				if resp != nil && resp.Body != nil {
					resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
				} else {
					cancel()
				}
			}
			t.observe(req, resp, err, attempt, start)
			return resp, err
		}
		jittered, next := computeBackoff(delay, t.maxDelay, t.rndFloat())
		sleep := jittered
		if resp != nil {
			if ra := retryAfter(resp.Header.Get("Retry-After")); ra > 0 {
				sleep = ra // honor server backoff exactly; no jitter
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
		}
		if cancel != nil {
			cancel() // attempt is done; body drained — release the per-attempt ctx
		}
		t.logRetry(req, resp, err, attempt, sleep)
		select {
		case <-req.Context().Done():
			t.observe(req, nil, req.Context().Err(), attempt, start)
			return nil, req.Context().Err()
		case <-time.After(sleep):
		}
		delay = next
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

// computeBackoff returns the equal-jittered sleep for the current delay and the
// next (doubled, capped) base delay. rnd must be in [0,1). Equal jitter keeps
// sleep in [delay/2, delay), so retries from collectors throttled together do
// not align.
func computeBackoff(delay, maxDelay time.Duration, rnd float64) (sleep, next time.Duration) {
	half := delay / 2
	sleep = half + time.Duration(rnd*float64(half))
	next = min(delay*2, maxDelay)
	return sleep, next
}

// rndFloat returns the jitter fraction in [0,1), using the injected source when
// set (tests) or math/rand/v2 otherwise.
func (t *retryTransport) rndFloat() float64 {
	if t.rnd != nil {
		return t.rnd()
	}
	return rand.Float64() //nolint:gosec // G404: backoff jitter is not security-sensitive (math/rand/v2)
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
