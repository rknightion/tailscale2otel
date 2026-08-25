package annotations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/httpguard"
)

// annotationsPath is the ONLY Grafana path this package ever calls. It is a
// constant rather than a parameter so a caller cannot repoint the client, which
// is what keeps "one outbound write, one destination" a structural property
// instead of a promise.
const annotationsPath = "/api/annotations"

// FailureCode is the bounded category of a publish failure. Bounded because it
// becomes a metric label, and a raw error string there is unbounded cardinality.
type FailureCode string

const (
	// FailureUnauthorized is 401/403 — the token is missing, expired, or lacks
	// annotations:create. The one an operator most needs named.
	FailureUnauthorized FailureCode = "unauthorized"
	// FailureRateLimited is 429 from Grafana itself, distinct from this
	// process's own max_per_minute ceiling, which never reaches the wire.
	FailureRateLimited FailureCode = "rate_limited"
	// FailureRejected is any other 4xx — a malformed request.
	FailureRejected FailureCode = "rejected"
	// FailureServerError is 5xx.
	FailureServerError FailureCode = "server_error"
	// FailureTransport is a connection, DNS, TLS or timeout failure: the
	// request never got an HTTP status.
	FailureTransport FailureCode = "transport"
)

// FailureCodes returns every failure code in a stable order, so a test can
// enumerate the closed set rather than restate it.
func FailureCodes() []FailureCode {
	return []FailureCode{
		FailureUnauthorized, FailureRateLimited, FailureRejected,
		FailureServerError, FailureTransport,
	}
}

// PublishError carries a bounded code plus local transport detail. Peer response
// bodies never reach Detail or Error because a server can reflect request
// credentials.
type PublishError struct {
	Code FailureCode
	// Status is the HTTP status, or 0 when the request never got one.
	Status int
	// RetryAfter is the parsed Retry-After header: 0 when absent or
	// unparseable, and a nanosecond when the server said "retry immediately" (a
	// zero, negative or past value). The two must stay distinguishable —
	// treating "retry now" as "no header" substitutes an exponential wait the
	// server never asked for.
	RetryAfter time.Duration
	Detail     string
}

func (e *PublishError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("grafana annotation write failed (%s, HTTP %d)", e.Code, e.Status)
	}
	return fmt.Sprintf("grafana annotation write failed (%s): %s", e.Code, e.Detail)
}

// ClientConfig is the resolved transport configuration for the writer.
type ClientConfig struct {
	// URL is the Grafana base URL. A trailing slash is tolerated.
	URL string
	// Token authenticates as a service account holding annotations:create.
	Token string
	// DashboardUID confines annotations to one dashboard. Empty publishes
	// organization annotations, visible to every board — the default, and the
	// point of pushing them rather than deriving them on one dashboard.
	DashboardUID string
	Timeout      time.Duration
}

// Client posts annotations to one Grafana organization.
//
// It deliberately offers no TLS-verification escape hatch. This is the
// process's only outbound write and it carries a token that can create
// annotations in the org, so an operator behind a private CA should trust the
// CA (SSL_CERT_FILE / SSL_CERT_DIR) rather than disable verification for it.
type Client struct {
	endpoint     string
	token        string
	dashboardUID string
	http         *http.Client
	// now is injectable because an HTTP-date Retry-After is only meaningful
	// against a clock, and a test that had to sleep to observe backoff would be
	// both slow and flaky.
	now func() time.Time
}

// NewClient builds the annotation client from the validated config.
func NewClient(cfg ClientConfig) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.URL), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errors.New("grafana_annotations.url is not a valid absolute URL")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("grafana_annotations.url must be http or https, got scheme %q", base.Scheme)
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, errors.New("grafana_annotations.url must contain only a scheme and host; put credentials in the token field")
	}
	if base.Scheme != "https" && !httpguard.IsLoopbackHost(base.Host) {
		return nil, errors.New("grafana_annotations.url must use HTTPS except for a loopback development endpoint")
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New("grafana_annotations: no service-account token (set token or token_file)")
	}
	return &Client{
		endpoint:     base.String() + annotationsPath,
		token:        token,
		dashboardUID: strings.TrimSpace(cfg.DashboardUID),
		http:         httpguard.NoRedirectClient(&http.Client{Timeout: cfg.Timeout}),
		now:          time.Now,
	}, nil
}

// wireAnnotation is the POST /api/annotations body. Times are epoch
// MILLISECONDS — the API's documented unit, and the one thing about this
// endpoint that is silently wrong rather than rejected if you send seconds: a
// second-resolution value lands in 1970, the annotation is invisible on every
// dashboard, and the API cheerfully returns 200.
type wireAnnotation struct {
	DashboardUID string   `json:"dashboardUID,omitempty"`
	Time         int64    `json:"time"`
	TimeEnd      int64    `json:"timeEnd,omitempty"`
	Tags         []string `json:"tags"`
	Text         string   `json:"text"`
}

// payloadFor renders one annotation into the wire body. Separate from Publish
// so the body is testable without an HTTP round trip.
//
// extraTags are the operator's own tags, appended HERE rather than inside
// Annotation.Tags so the tag contract stays a pure function of the annotation
// and a test can assert the contract without configuration in scope.
func (c *Client) payloadFor(a Annotation, extraTags []string) wireAnnotation {
	body := wireAnnotation{
		DashboardUID: c.dashboardUID,
		Time:         a.Time.UnixMilli(),
		Tags:         append(a.Tags(), extraTags...),
		Text:         a.Text,
	}
	if !a.TimeEnd.IsZero() {
		body.TimeEnd = a.TimeEnd.UnixMilli()
	}
	return body
}

// Publish writes one annotation, returning a *PublishError on failure so the
// caller can record a bounded code without inspecting the error string.
func (c *Client) Publish(ctx context.Context, a Annotation, extraTags []string) error {
	payload, err := json.Marshal(c.payloadFor(a, extraTags))
	if err != nil {
		return &PublishError{Code: FailureRejected, Detail: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return &PublishError{Code: FailureTransport, Detail: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return &PublishError{Code: FailureTransport, Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	// Bounded read: a misconfigured URL pointing at something that is not
	// Grafana must not stream an unbounded body into the process. Draining also
	// lets the connection be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return &PublishError{
		Code:       classify(resp.StatusCode),
		Status:     resp.StatusCode,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), c.now()),
	}
}

// classify maps an HTTP status onto the bounded failure code.
func classify(status int) FailureCode {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return FailureUnauthorized
	case status == http.StatusTooManyRequests:
		return FailureRateLimited
	case status >= 500:
		return FailureServerError
	default:
		return FailureRejected
	}
}

// Rate-limit backoff. Deliberately not config keys: an operator has no way to
// choose these better than the server's own Retry-After, which takes precedence
// whenever present, and a tunable backoff is a tunable way to keep hammering a
// limit shared by the whole Grafana org.
const (
	// backoffBase is the first wait, doubled per consecutive rate-limited
	// write.
	backoffBase = 30 * time.Second
	// backoffCeiling bounds both the exponential wait and any Retry-After the
	// server states. Past this the writer is no longer usefully "retrying
	// later", it is off — and an operator reading a flat published counter
	// needs the failure to stay visibly recurring on the dropped counter.
	backoffCeiling = 15 * time.Minute
	// backoffJitter is the ±fraction applied to the exponential wait. Two
	// deployments against one Grafana org share its rate limit, so an
	// unjittered 30/60/120s ladder makes them retry in lockstep and re-trigger
	// the limit together. Never applied to a server-stated Retry-After: that
	// instant was chosen for us.
	backoffJitter = 0.2
	// maxBackoffStreak caps the doubling exponent. Without it the shift
	// overflows on a long outage, and 2^5 * 30s already exceeds the ceiling.
	maxBackoffStreak = 6
)

// parseRetryAfter reads RFC 9110's Retry-After in BOTH legal forms. Grafana
// Cloud sends delta-seconds; a proxy in front of a self-hosted Grafana may send
// an HTTP-date, and parsing only the integer form silently discards the
// server's instruction in favor of a guess. Garbage returns 0 (no usable
// header) rather than an error: a malformed header must never be able to stall
// or short-circuit the writer.
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	clamp := func(d time.Duration) time.Duration {
		if d <= 0 {
			// Legal, and means "retry now". Returned as a nanosecond so it
			// stays distinct from the "absent" zero.
			return time.Nanosecond
		}
		// A proxy answering 429 with a ten-day Retry-After would otherwise park
		// the writer past any plausible operator attention span.
		if d > backoffCeiling {
			return backoffCeiling
		}
		return d
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		return clamp(time.Duration(seconds) * time.Second)
	}
	if at, err := http.ParseTime(header); err == nil {
		return clamp(at.Sub(now))
	}
	return 0
}

// backoffDelay returns the wait for a rate-limited write. A server-stated
// Retry-After always wins: it is the only number in play that describes the
// actual limit rather than guessing at it.
func backoffDelay(retryAfter time.Duration, streak int) (time.Duration, string) {
	if retryAfter > 0 {
		return retryAfter, "retry-after"
	}
	if streak < 1 {
		streak = 1
	}
	streak = min(streak, maxBackoffStreak)
	delay := min(backoffBase*time.Duration(1<<(streak-1)), backoffCeiling)
	return jitter(delay), "exponential"
}

// jitter spreads a wait by ±backoffJitter.
func jitter(delay time.Duration) time.Duration {
	spread := int64(float64(delay) * backoffJitter)
	if spread <= 0 {
		return delay
	}
	return delay - time.Duration(spread) + time.Duration(rand.Int64N(2*spread+1)) //nolint:gosec // G404: backoff jitter is not security-sensitive (math/rand/v2)
}

// rateLimiter is a token bucket sized in annotations per minute. It is a
// CEILING on what reaches Grafana, not a queue: over the ceiling an annotation
// is dropped and counted, because delaying a marker past the moment it explains
// makes it worse than absent.
type rateLimiter struct {
	interval time.Duration
	capacity int
	tokens   int
	last     time.Time
	now      func() time.Time
}

func newRateLimiter(perMinute int, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	if perMinute < 1 {
		perMinute = 1
	}
	return &rateLimiter{
		interval: time.Minute / time.Duration(perMinute),
		capacity: perMinute,
		tokens:   perMinute,
		last:     now(),
		now:      now,
	}
}

// allow reports whether one annotation may be written now.
func (l *rateLimiter) allow() bool {
	now := l.now()
	if elapsed := now.Sub(l.last); elapsed >= l.interval {
		refill := int(elapsed / l.interval)
		l.tokens = min(l.capacity, l.tokens+refill)
		l.last = now
	}
	if l.tokens <= 0 {
		return false
	}
	l.tokens--
	return true
}
