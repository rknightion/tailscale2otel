package tsapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"golang.org/x/oauth2"
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
	// of each logical request with the span-carrying context (for trace-exemplar
	// linkage) and a RequestInfo describing the outcome.
	onRequest func(context.Context, RequestInfo)

	// logger, when non-nil, records status-aware retry/outcome events: 429
	// backoff at INFO, 5xx/transport backoff at DEBUG, an auth failure at ERROR.
	logger *slog.Logger

	// tracer, when non-nil, emits one child span per logical request. A nil
	// tracer is replaced with a no-op at span-start so RoundTrip needs no guard.
	tracer trace.Tracer
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

// logRetry records a backoff before a retry: 429 at INFO (otherwise invisible —
// a throttled-then-recovered request produces no error), 5xx/transport at DEBUG.
func (t *retryTransport) logRetry(req *http.Request, resp *http.Response, err error, attempt int, sleep time.Duration) {
	if t.logger == nil {
		return
	}
	ep := endpointLabel(req.URL.Path)
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		t.logger.Info("tailscale API rate limited; backing off",
			"endpoint", ep, "attempt", attempt, "status", resp.StatusCode, "sleep", sleep)
		return
	}
	args := []any{"endpoint", ep, "attempt", attempt, "sleep", sleep}
	if resp != nil {
		args = append(args, "status", resp.StatusCode)
	}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	t.logger.Debug("retrying tailscale API request", args...)
}

// logFinal records the terminal outcome of a request. Only an unambiguous auth
// failure is logged, at ERROR: a 401 HTTP response (mainly the API-key path) or
// an OAuth token request that failed with 401/403 (the OAuth path returns this
// as a transport error, not an HTTP response). Other statuses are owned by the
// collector scheduler (avoids per-tick 4xx spam and double-logging exhausted
// retries).
func (t *retryTransport) logFinal(req *http.Request, resp *http.Response, err error, attempt int) {
	if t.logger == nil {
		return
	}
	ep := endpointLabel(req.URL.Path)
	switch {
	case resp != nil && resp.StatusCode == http.StatusUnauthorized:
		t.logger.Error("tailscale API request unauthorized; check credentials",
			"endpoint", ep, "attempts", attempt)
	case err != nil:
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil &&
			(re.Response.StatusCode == http.StatusUnauthorized || re.Response.StatusCode == http.StatusForbidden) {
			t.logger.Error("tailscale OAuth token request failed; check client credentials",
				"endpoint", ep, "attempts", attempt, "status", re.Response.StatusCode)
		}
	}
}

// noopAPITracer is the shared fallback for a nil retryTransport.tracer, so the
// nil-tracer path allocates no tracer per RoundTrip.
var noopAPITracer = tracenoop.NewTracerProvider().Tracer("")

// startSpan starts a child span for one logical API request. If t.tracer is nil,
// a no-op tracer is used so RoundTrip never needs a nil-guard.
func (t *retryTransport) startSpan(ctx context.Context, req *http.Request) (context.Context, trace.Span) {
	tr := t.tracer
	if tr == nil {
		tr = noopAPITracer
	}
	return tr.Start(ctx, "tailscale.api "+endpointLabel(req.URL.Path),
		trace.WithSpanKind(trace.SpanKindClient))
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	spanCtx, span := t.startSpan(req.Context(), req)
	var (
		resp  *http.Response
		err   error
		delay = t.baseDelay
	)
	for attempt := 1; ; attempt++ {
		actx := spanCtx
		var cancel context.CancelFunc
		if t.attemptTimeout > 0 {
			actx, cancel = context.WithTimeout(spanCtx, t.attemptTimeout)
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
				t.observe(spanCtx, req, nil, gbErr, attempt, start, span)
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
			t.observe(spanCtx, req, resp, err, attempt, start, span)
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
		if span.IsRecording() {
			ev := []attribute.KeyValue{
				attribute.Int("attempt", attempt),
				attribute.Int64("sleep_ms", sleep.Milliseconds()),
			}
			if resp != nil {
				ev = append(ev, attribute.Int("http.response.status_code", resp.StatusCode))
			}
			span.AddEvent("retry", trace.WithAttributes(ev...))
		}
		t.logRetry(req, resp, err, attempt, sleep)
		select {
		case <-spanCtx.Done():
			t.observe(spanCtx, req, nil, spanCtx.Err(), attempt, start, span)
			return nil, spanCtx.Err()
		case <-time.After(sleep):
		}
		delay = next
	}
}

// observe finalizes the span for a completed logical request (sets attributes,
// status, and ends it), then calls the onRequest hook with the span-carrying
// context so exemplars can link to the ended span. The span's SpanContext
// remains in spanCtx after End(), so the hook's ctx carries the trace/span IDs.
func (t *retryTransport) observe(spanCtx context.Context, req *http.Request, resp *http.Response, err error, attempts int, start time.Time, span trace.Span) {
	status := 0
	if err == nil && resp != nil {
		status = resp.StatusCode
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	if span.IsRecording() {
		// §0.2 tier-2 useful identifiers: the full path carries the tailnet name +
		// device id, so an operator can see WHICH device's request was slow/failed
		// (the endpointLabel span name elides it). Tailscale puts no secret in the
		// URL (auth is a Bearer header), so url.full is safe. NOT the response body
		// (multi-MB on the flow-log pull; already decoded into metrics+logs).
		span.SetAttributes(
			attribute.String("tailscale.endpoint", endpointLabel(req.URL.Path)),
			attribute.String("url.full", req.URL.String()),
			attribute.String("http.request.method", req.Method),
			attribute.String("server.address", req.URL.Host),
			attribute.Int("http.request.resend_count", attempts-1),
		)
		if status != 0 {
			span.SetAttributes(attribute.Int("http.response.status_code", status))
		}
		switch {
		case err != nil:
			span.RecordError(err)
			span.SetStatus(codes.Error, errStr)
		case status >= 400:
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	}
	span.End()
	if t.onRequest != nil {
		t.onRequest(spanCtx, RequestInfo{
			Endpoint: endpointLabel(req.URL.Path),
			Status:   status,
			Attempts: attempts,
			Duration: time.Since(start),
			Err:      errStr,
		})
	}
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
