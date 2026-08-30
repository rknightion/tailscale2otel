package tsapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/httpretry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"golang.org/x/oauth2"
)

// authKeyTransport injects a Bearer token on each request bound for the
// configured Tailscale origin.
//
// #466: this used to set the header unconditionally. http.Client strips a
// cross-origin Authorization header while following a redirect — but it then
// calls the transport again for the redirected request, and an unconditional
// RoundTrip put the credential straight back on. Binding attachment to
// Options.BaseURL's scheme+authority means the key cannot ride a redirect (or
// any other off-origin request) out of the tailnet's API. redirectPolicy
// refuses such a redirect outright; this is the second line of defense for any
// caller that supplies its own http.Client around the transport.
//
// origin is REQUIRED: the zero value matches nothing, so a mis-wired transport
// fails closed (unauthenticated 401s) rather than open.
type authKeyTransport struct {
	base   http.RoundTripper
	key    string
	origin origin
}

func (t *authKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !originOf(req.URL).equal(t.origin) {
		return t.base.RoundTrip(req)
	}
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
	Duration time.Duration // wall-clock of the whole logical request (incl. retries/backoff), EXCLUDING WaitDuration (#76)
	// WaitDuration is the cumulative time this logical request spent blocked on
	// the client-side rate limiter (Options.RateLimit), summed across every
	// attempt including retries. Zero when RateLimit is unconfigured or a token
	// was always immediately available. Kept separate from Duration so
	// self-observability can distinguish "queued behind our own rate limit"
	// from genuine API/network latency and retry backoff (#76).
	WaitDuration time.Duration
	Err          string // transport error text, "" when an HTTP response was received
}

// retryTransport retries 429 and 5xx responses, and transport errors that
// classifyTransportError judges retryable, with exponential backoff, honoring
// Retry-After. A terminal transport error — most importantly a token endpoint
// answering 401/403 — costs exactly one attempt (#489). Safe for the
// idempotent, bodyless GETs used by this package. See retryableOutcome.
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
	// sleeps are NOT bounded by it — they wait on the parent request context.
	// A server-specified Retry-After is honored up to maxDelay (#206): a
	// longer value is clamped rather than parking the request for the full
	// duration, since most collectors share an app-lifetime parent context.
	attemptTimeout time.Duration

	// limiter, when non-nil, gates each attempt on a rate-limiter token. The wait
	// is performed on the PARENT request context, BEFORE attemptTimeout is applied,
	// so time spent queueing for a token does not consume the per-attempt HTTP
	// timeout (which bounds only connect/headers/body I/O). Nil disables rate
	// limiting (pass-through).
	limiter rateWaiter

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
		// #468: NEVER err.Error() here. oauth2.RetrieveError.Error() embeds the RAW
		// token-endpoint response body, so the DEBUG retry path used to write a
		// hostile (or merely verbose) body straight into local logs, bypassing the
		// sanitization telemetry and the status page already went through.
		args = append(args, diagnosticLogArgs(err)...)
	}
	t.logger.Debug("retrying tailscale API request", args...)
}

// diagnosticLogArgs renders a transport error as bounded slog key/values. It is
// the ONLY way an error reaches a log line in this package (#468), so the raw
// response body, headers, client secret, assertion and token-URL userinfo have
// no path in — including via %v/%+v/%w formatting or slog.Any, none of which
// are used on the error value itself.
func diagnosticLogArgs(err error) []any {
	d := classifyTransportError(err)
	args := []any{"error_class", d.Class, "error_retryable", d.Retryable, "error", d.Message}
	if d.Status != 0 {
		args = append(args, "error_status", d.Status)
	}
	return args
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
			// Same sanitized representation as the retry path and telemetry (#468):
			// the status arrives as error_status, never via the raw error text.
			args := append([]any{"endpoint", ep, "attempts", attempt}, diagnosticLogArgs(err)...)
			t.logger.Error("tailscale OAuth token request failed; check client credentials", args...)
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
		resp      *http.Response
		err       error
		delay     = t.baseDelay
		waitTotal time.Duration // cumulative rate-limiter wait, all attempts (#76)
	)
	for attempt := 1; ; attempt++ {
		// Acquire a rate-limiter token before starting the attempt. This waits on
		// the parent context (spanCtx), NOT the per-attempt timeout context, so a
		// long queue wait is never misreported as an HTTP attempt timeout. Every
		// attempt — including retries — acquires its own token (backpressure). The
		// wait is timed and accumulated into waitTotal so it can be reported
		// separately from (and excluded from) the request's Duration (#76).
		if t.limiter != nil {
			waitStart := time.Now()
			werr := t.limiter.Wait(spanCtx)
			waitTotal += time.Since(waitStart)
			if werr != nil {
				t.observe(spanCtx, req, nil, werr, attempt, start, waitTotal, span)
				return nil, werr
			}
		}
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
				t.observe(spanCtx, req, nil, gbErr, attempt, start, waitTotal, span)
				return nil, gbErr
			}
			attemptReq.Body = body
		}
		resp, err = t.base.RoundTrip(attemptReq)
		retryable := retryableOutcome(resp, err)
		if !retryable || attempt >= t.max {
			t.logFinal(req, resp, err, attempt)
			if cancel != nil {
				if resp != nil && resp.Body != nil {
					resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
				} else {
					cancel()
				}
			}
			t.observe(spanCtx, req, resp, err, attempt, start, waitTotal, span)
			return resp, err
		}
		jittered, next := httpretry.ComputeBackoff(delay, t.maxDelay, t.rndFloat())
		sleep := jittered
		if resp != nil {
			if ra := httpretry.RetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
				sleep = min(ra, t.maxDelay) // honor server backoff exactly, capped at maxDelay (#206)
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
			t.observe(spanCtx, req, nil, spanCtx.Err(), attempt, start, waitTotal, span)
			return nil, spanCtx.Err()
		case <-time.After(sleep):
		}
		delay = next
	}
}

// retryableOutcome decides whether one attempt's outcome is worth another try.
//
// HTTP responses keep the original rule unchanged (#489): 429 and 5xx are
// transient, every other status is final. The retry loop never second-guesses a
// status the server actually returned.
//
// Transport errors — no HTTP response at all — defer to classifyTransportError's
// Retryable judgement instead of the old "err != nil, so retry" blanket. That
// blanket meant an OAuth token fetch rejected with 401/403 (wrong client
// credentials, revoked client) was retried max_attempts times with full backoff
// on every collector tick, even though the transport already knew the failure
// was terminal and said so in the error_retryable log field (#489). Only errors
// the classifier judges terminal stop early:
//
//   - oauth_* with a non-429, sub-500 token-endpoint status, or an RFC 6749
//     error code — the endpoint rejected the grant; a replay is rejected too.
//   - redirect_refused — the redirect policy's verdict (#466/#467) is a pure
//     function of the response, so retrying reproduces it exactly.
//   - canceled — the parent context is gone. This already cost exactly one
//     attempt (the backoff select fires on spanCtx.Done() immediately); the
//     only change is that the misleading "retrying" DEBUG record is gone.
//
// Everything transient stays retryable: network errors, timeouts (including the
// per-attempt deadline from attemptTimeout), and 429/5xx from the token
// endpoint. An error the classifier cannot place falls through to the network
// class, which is retryable — unrecognized means "might be transient", never
// "terminal".
func retryableOutcome(resp *http.Response, err error) bool {
	if err != nil {
		return classifyTransportError(err).Retryable
	}
	return httpretry.RetryableOutcome(resp, nil)
}

// computeBackoff preserves the package-private test seam while delegating the
// provider-neutral calculation to internal/httpretry.
func computeBackoff(delay, maxDelay time.Duration, rnd float64) (sleep, next time.Duration) {
	return httpretry.ComputeBackoff(delay, maxDelay, rnd)
}

// retryAfter preserves the package-private test seam while delegating the
// provider-neutral parsing to internal/httpretry.
func retryAfter(h string) time.Duration { return httpretry.RetryAfter(h) }

// observe finalizes the span for a completed logical request (sets attributes,
// status, and ends it), then calls the onRequest hook with the span-carrying
// context so exemplars can link to the ended span. The span's SpanContext
// remains in spanCtx after End(), so the hook's ctx carries the trace/span IDs.
// waitTotal is the cumulative rate-limiter wait across all attempts (#76); it
// is reported separately as RequestInfo.WaitDuration and subtracted out of
// RequestInfo.Duration so onRequest observers (and the api.duration histogram
// built from Duration) see genuine API/network + backoff latency only.
func (t *retryTransport) observe(spanCtx context.Context, req *http.Request, resp *http.Response, err error, attempts int, start time.Time, waitTotal time.Duration, span trace.Span) {
	status := 0
	if err == nil && resp != nil {
		status = resp.StatusCode
	}
	errStr := ""
	if err != nil {
		errStr = sanitizeTransportError(err)
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
		if waitTotal > 0 {
			span.SetAttributes(attribute.Int64("tailscale.rate_limit.wait_ms", waitTotal.Milliseconds()))
		}
		if status != 0 {
			span.SetAttributes(attribute.Int("http.response.status_code", status))
		}
		switch {
		case err != nil:
			span.RecordError(errors.New(errStr))
			span.SetStatus(codes.Error, errStr)
		case status >= 400:
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	}
	span.End()
	if t.onRequest != nil {
		// total includes waitTotal (start predates the first limiter Wait), so
		// subtracting yields the non-limiter portion of the request's latency.
		// waitTotal can never exceed total in practice, but clamp defensively
		// against a future refactor reordering the timers.
		total := time.Since(start)
		duration := total - waitTotal
		if duration < 0 {
			duration = 0
		}
		t.onRequest(spanCtx, RequestInfo{
			Endpoint:     endpointLabel(req.URL.Path),
			Status:       status,
			Attempts:     attempts,
			Duration:     duration,
			WaitDuration: waitTotal,
			Err:          errStr,
		})
	}
}

// Stable, bounded diagnostic classes for transport and token errors (#468).
// The set is closed: every error resolves to one of these, so the class is safe
// as a log field, a span attribute or a metric dimension.
const (
	errClassOAuth           = "oauth_error"      // token endpoint failed (generic / unrecognized code)
	errClassRedirectRefused = "redirect_refused" // cross-origin redirect or redirect-loop cap (#466/#467)
	errClassTimeout         = "timeout"
	errClassCanceled        = "canceled"
	errClassNetwork         = "network"
)

// rfc6749ErrorCodes is the CLOSED set of OAuth error codes promoted to their
// own "oauth_<code>" class. The token endpoint chooses this string, so anything
// outside the RFC folds into errClassOAuth — an endpoint (or a proxy in front
// of it) must not be able to mint unbounded diagnostic classes.
var rfc6749ErrorCodes = map[string]bool{
	"invalid_request":        true,
	"invalid_client":         true,
	"invalid_grant":          true,
	"unauthorized_client":    true,
	"unsupported_grant_type": true,
	"invalid_scope":          true,
}

// maxDiagnosticMessage bounds the sanitized message. Endpoint-supplied fields
// (an OAuth error_description, a vendor error code) are structured, not raw
// body — but they are still remote input, so they are capped and flattened to
// one line before they can reach a log record or a span status.
const maxDiagnosticMessage = 200

// transportDiagnostic is the sanitized, bounded view of a transport error: a
// stable class, the token-endpoint status when there was one, a retryability
// judgement, and a message safe to log at any level (#468).
//
// Retryable is AUTHORITATIVE for the retry loop's transport-error path as of
// #489: retryTransport asks retryableOutcome, which returns this field verbatim
// whenever there is no HTTP response, so a 401 from the token endpoint stops
// after one attempt instead of burning max_attempts of backoff per collector
// tick. It is still reported as the error_retryable log field so an operator
// reading a log can tell "this will not improve by trying again" from
// "transient" — the field and the behavior can no longer disagree. HTTP
// responses do NOT consult it (429/5xx retry, everything else is final).
type transportDiagnostic struct {
	Class     string
	Status    int
	Retryable bool
	Message   string
}

// sanitizeTransportError renders err for telemetry (span status, RecordError,
// RequestInfo.Err, the admin status page). It is the Message of the shared
// diagnostic, so telemetry, retry logs and terminal logs all render the SAME
// sanitized text (#468).
func sanitizeTransportError(err error) string {
	return classifyTransportError(err).Message
}

// classifyTransportError converts err into its bounded diagnostic. Nothing that
// reaches a log, a span or the status page bypasses this function.
func classifyTransportError(err error) transportDiagnostic {
	if err == nil {
		return transportDiagnostic{}
	}
	// Unwrap url.Error wrappers first so errors.As can reach the inner type, and
	// so the (userinfo-stripped) endpoint identity survives in the message.
	var ue *url.Error
	if errors.As(err, &ue) {
		d := classifyTransportError(ue.Err)
		if d.Class == "" { // url.Error with a nil inner error: still needs a class
			d.Class, d.Retryable = errClassNetwork, true
		}
		d.Message = safeDiagnosticText(ue.Op + " " + redactURL(ue.URL) + ": " + d.Message)
		return d
	}
	var cor *CrossOriginRedirectError
	if errors.As(err, &cor) {
		return transportDiagnostic{Class: errClassRedirectRefused, Message: safeDiagnosticText(cor.Error())}
	}
	if errors.Is(err, ErrCrossOriginRedirect) || errors.Is(err, ErrTooManyRedirects) {
		return transportDiagnostic{Class: errClassRedirectRefused, Message: safeDiagnosticText(err.Error())}
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return classifyOAuthRetrieveError(re)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return transportDiagnostic{Class: errClassCanceled, Message: safeDiagnosticText(err.Error())}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return transportDiagnostic{Class: errClassTimeout, Retryable: true, Message: safeDiagnosticText(err.Error())}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return transportDiagnostic{Class: errClassTimeout, Retryable: true, Message: safeDiagnosticText(err.Error())}
	}
	return transportDiagnostic{Class: errClassNetwork, Retryable: true, Message: safeDiagnosticText(err.Error())}
}

// classifyOAuthRetrieveError uses ONLY the structured fields of a
// RetrieveError. Its Body — the raw token-endpoint response — is never read
// here, and Error() (which embeds that body) is never called.
func classifyOAuthRetrieveError(re *oauth2.RetrieveError) transportDiagnostic {
	d := transportDiagnostic{Class: errClassOAuth}
	if re.Response != nil {
		d.Status = re.Response.StatusCode
	}
	d.Retryable = d.Status == http.StatusTooManyRequests || d.Status >= 500
	switch {
	case re.ErrorCode != "":
		if rfc6749ErrorCodes[re.ErrorCode] {
			d.Class = "oauth_" + re.ErrorCode
		}
		msg := "oauth2: " + re.ErrorCode
		if re.ErrorDescription != "" {
			msg += ": " + re.ErrorDescription
		}
		d.Message = safeDiagnosticText(msg)
	case re.Response != nil:
		d.Message = safeDiagnosticText("oauth2: cannot fetch token: " + re.Response.Status)
	default:
		d.Message = "oauth2: cannot fetch token"
	}
	return d
}

// redactURL strips userinfo from a URL string so a token URL configured as
// https://client:secret@host/... cannot reach a log, span or status page (#468).
// An unparseable URL is dropped entirely rather than echoed.
func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparseable url)"
	}
	if u.User != nil {
		u.User = url.User("redacted")
	}
	return u.String()
}

// safeDiagnosticText flattens a diagnostic message to a single bounded line:
// newlines/tabs collapse to spaces (a multiline body must not be able to forge
// extra log records or split a span status) and the result is truncated.
func safeDiagnosticText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxDiagnosticMessage {
		s = s[:maxDiagnosticMessage] + "..."
	}
	return s
}

// collectionsWithVarSegment is the set of collection prefixes (already with the
// tailnet and api/v2 prefixes removed) whose immediately following path segment
// is variable and must be elided for low-cardinality labeling. Longer prefixes
// are listed first so the scan short-circuits correctly.
//
// Pattern: <collection>/{var} → <collection>
//
//	<collection>/{var}/<leaf…> → <collection>/<leaf…>
var collectionsWithVarSegment = []string{
	"posture/integrations", // /api/v2/posture/integrations/{id}[/…]
	"services",             // /api/v2/tailnet/{t}/services/{name}[/…]
	"keys",                 // /api/v2/tailnet/{t}/keys/{id}
	"users",                // /api/v2/tailnet/{t}/users/{id} (tsclient Users().Get)
	"webhooks",             // /api/v2/webhooks/{id}[/…]
}

// elideVarSegment checks whether s starts with a known collection prefix
// followed by a variable segment and elides that segment:
//
//	services/svc:argocd/devices → services/devices
//	keys/kfoo123               → keys
//
// It returns s unchanged when no prefix matches.
func elideVarSegment(s string) string {
	for _, coll := range collectionsWithVarSegment {
		rest, ok := strings.CutPrefix(s, coll+"/")
		if !ok {
			continue
		}
		// rest is now "{var}" or "{var}/leaf…"; drop the variable segment.
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return coll + "/" + rest[i+1:]
		}
		return coll
	}
	return s
}

// endpointLabel derives a stable, low-cardinality label from an API request
// path by stripping the "/api/v2/tailnet/{tailnet}/" prefix, e.g. "devices",
// "logging/network" or "user-invites". Non-tailnet paths get a short stable
// label: "oauth/token" or, for per-device endpoints, "device/{leaf}" with the
// variable device id elided. Variable per-item segments in named collection
// paths (services/{name}/…, keys/{id}, users/{id}, webhooks/{id}/…,
// posture/integrations/{id}) are also elided to keep metric label cardinality
// bounded.
func endpointLabel(p string) string {
	p = strings.Trim(p, "/")
	p = strings.TrimPrefix(p, "api/v2/")
	if rest, ok := strings.CutPrefix(p, "tailnet/"); ok {
		// Drop the tailnet segment.
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return elideVarSegment(rest[i+1:])
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
	return elideVarSegment(p)
}

// rndFloat returns the jitter fraction in [0,1), using the injected source when
// set (tests) or math/rand/v2 otherwise.
func (t *retryTransport) rndFloat() float64 {
	if t.rnd != nil {
		return t.rnd()
	}
	return rand.Float64() //nolint:gosec // G404: backoff jitter is not security-sensitive (math/rand/v2)
}
