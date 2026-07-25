package tsapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// oauthErr builds a token-endpoint failure exactly as x/oauth2 produces one for
// a non-2xx token response: a *oauth2.RetrieveError carrying the response (and
// therefore its status), returned unwrapped through oauth2.Transport.RoundTrip.
func oauthErr(status int) error {
	return &oauth2.RetrieveError{
		Response: &http.Response{StatusCode: status, Status: http.StatusText(status)},
		Body:     []byte(`{"error":"invalid_client"}`),
	}
}

// TestRetryTransport_TransportErrorRetryFollowsClassifier pins #489: the retry
// loop's transport-error decision is classifyTransportError's Retryable
// judgement, not the old "err != nil means retry" blanket. A token endpoint
// that answers 401/403 (bad client credentials) can never succeed by trying
// again, so it must cost exactly ONE attempt and no backoff — previously it
// burned max_attempts with full backoff on every collector tick.
//
// Everything genuinely transient — a network error, a timeout (including the
// per-attempt deadline), a 429 or 5xx from the token endpoint — must keep
// retrying to exhaustion exactly as before.
func TestRetryTransport_TransportErrorRetryFollowsClassifier(t *testing.T) {
	const maxAttempts = 4
	cases := []struct {
		name      string
		err       error
		wantCalls int
	}{
		// --- terminal: stop after one attempt (the #489 change) ---
		{"oauth token 401 is terminal", oauthErr(http.StatusUnauthorized), 1},
		{"oauth token 403 is terminal", oauthErr(http.StatusForbidden), 1},
		{"oauth token 400 is terminal", oauthErr(http.StatusBadRequest), 1},
		{
			"oauth token 401 wrapped in url.Error is terminal",
			&url.Error{
				Op:  "Post",
				URL: "https://api.tailscale.com/api/v2/oauth/token",
				Err: oauthErr(http.StatusUnauthorized),
			},
			1,
		},
		{
			"oauth RFC6749 invalid_client without a response is terminal",
			&oauth2.RetrieveError{ErrorCode: "invalid_client", ErrorDescription: "bad client"},
			1,
		},

		// --- transient: unchanged, retried to exhaustion ---
		{"oauth token 429 still retries", oauthErr(http.StatusTooManyRequests), maxAttempts},
		{"oauth token 500 still retries", oauthErr(http.StatusInternalServerError), maxAttempts},
		{"oauth token 503 still retries", oauthErr(http.StatusServiceUnavailable), maxAttempts},
		{"plain network error still retries", errors.New("dial tcp: connection refused"), maxAttempts},
		{"context deadline still retries", context.DeadlineExceeded, maxAttempts},
		{"os deadline still retries", os.ErrDeadlineExceeded, maxAttempts},
		{
			"url.Error wrapping a network error still retries",
			&url.Error{Op: "Get", URL: "https://api.tailscale.com/api/v2/tailnet/x/devices", Err: errors.New("EOF")},
			maxAttempts,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := &errRoundTripper{err: c.err}
			var got RequestInfo
			rt := &retryTransport{
				base:      base,
				max:       maxAttempts,
				baseDelay: time.Millisecond,
				maxDelay:  2 * time.Millisecond,
				rnd:       func() float64 { return 0 },
				onRequest: func(_ context.Context, i RequestInfo) { got = i },
			}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
				"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
			resp, err := rt.RoundTrip(req)
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("RoundTrip err = nil, want the transport error surfaced to the caller")
			}
			if base.calls != c.wantCalls {
				t.Fatalf("base attempts = %d, want %d", base.calls, c.wantCalls)
			}
			if got.Attempts != c.wantCalls {
				t.Fatalf("RequestInfo.Attempts = %d, want %d", got.Attempts, c.wantCalls)
			}
		})
	}
}

// TestRetryTransport_TerminalTokenErrorBurnsNoBackoff proves the #489 win is a
// timing win, not just an attempt-count one: a terminal 401 must return without
// sleeping through a single backoff interval. baseDelay here is large enough
// that even one retry would be unmistakable.
func TestRetryTransport_TerminalTokenErrorBurnsNoBackoff(t *testing.T) {
	base := &errRoundTripper{err: oauthErr(http.StatusUnauthorized)}
	rt := &retryTransport{
		base:      base,
		max:       5,
		baseDelay: 400 * time.Millisecond,
		maxDelay:  400 * time.Millisecond,
		rnd:       func() float64 { return 0 }, // sleep would be 200ms per retry
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	start := time.Now()
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip err = nil, want the token error")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("elapsed = %v, want no backoff at all on a terminal token failure", elapsed)
	}
	if base.calls != 1 {
		t.Fatalf("attempts = %d, want exactly 1", base.calls)
	}
}

// TestRetryTransport_HTTPResponseRetryRuleUnchanged pins the OTHER half of
// #489's scope: the HTTP-response path keeps its original rule (429 and 5xx
// retry, every other status is final) — the classifier governs transport errors
// only. 4xx statuses that reach us as a real HTTP response must still cost one
// attempt, and 429/5xx must still exhaust max_attempts.
func TestRetryTransport_HTTPResponseRetryRuleUnchanged(t *testing.T) {
	const maxAttempts = 3
	cases := []struct {
		name      string
		statuses  []int
		wantCalls int
	}{
		{"200 is final", []int{http.StatusOK}, 1},
		{"401 is final", []int{http.StatusUnauthorized}, 1},
		{"403 is final", []int{http.StatusForbidden}, 1},
		{"404 is final", []int{http.StatusNotFound}, 1},
		{"429 retries to exhaustion", []int{429, 429, 429}, maxAttempts},
		{"500 retries to exhaustion", []int{500, 500, 500}, maxAttempts},
		{"503 then 200 retries once", []int{503, http.StatusOK}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := &fakeRoundTripper{statuses: c.statuses}
			var got RequestInfo
			rt := &retryTransport{
				base:      base,
				max:       maxAttempts,
				baseDelay: time.Millisecond,
				maxDelay:  2 * time.Millisecond,
				rnd:       func() float64 { return 0 },
				onRequest: func(_ context.Context, i RequestInfo) { got = i },
			}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
				"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			_ = resp.Body.Close()
			if base.calls != c.wantCalls {
				t.Fatalf("attempts = %d, want %d", base.calls, c.wantCalls)
			}
			if got.Attempts != c.wantCalls {
				t.Fatalf("RequestInfo.Attempts = %d, want %d", got.Attempts, c.wantCalls)
			}
		})
	}
}

// TestRetryTransport_CanceledParentStillReturnsPromptly guards the behavior
// #489 must NOT change: when the parent context is gone the request returns
// immediately with a cancellation error after exactly one attempt. That already
// held before the change (the backoff select fires on spanCtx.Done()), and it
// still holds now that "canceled" classifies as non-retryable — the observable
// outcome is identical, minus one misleading "retrying" DEBUG record.
func TestRetryTransport_CanceledParentStillReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	rt := &retryTransport{
		base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: r.Context().Err()}
		}),
		max:       4,
		baseDelay: 2 * time.Second, // a single retry would be unmistakable
		maxDelay:  2 * time.Second,
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	start := time.Now()
	_, err := rt.RoundTrip(req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a context.Canceled error", err)
	}
	if calls != 1 {
		t.Fatalf("attempts = %d, want exactly 1", calls)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want an immediate return on a canceled parent", elapsed)
	}
}

// TestTokenRetryLogNeverLeaksResponseBody keeps #468's DEBUG *retry*-path leak
// proof alive after #489. TestTokenErrorLoggingNeverLeaksResponseBody drives a
// 401, which is now terminal and therefore never reaches logRetry; a 503 token
// error is still retried, so it exercises the retry record — the one that used
// to write oauth2.RetrieveError.Error() (raw response body included) straight
// into the log.
func TestTokenRetryLogNeverLeaksResponseBody(t *testing.T) {
	for name, tc := range oauthBodyLeakCases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			re := &oauth2.RetrieveError{
				Response: &http.Response{Status: "503 Service Unavailable", StatusCode: http.StatusServiceUnavailable},
				Body:     []byte(tc.body),
			}
			// The URL carries userinfo, which must never reach a log line either.
			wrapped := &url.Error{
				Op:  "Post",
				URL: "https://oauthclient:S3CR3T-CLIENT-SECRET@api.tailscale.com/api/v2/oauth/token",
				Err: re,
			}
			rt := &retryTransport{
				base:      &errRoundTripper{err: wrapped},
				max:       2, // 503 is retryable -> one retry -> logRetry fires
				baseDelay: time.Millisecond,
				maxDelay:  2 * time.Millisecond,
				rnd:       func() float64 { return 0 },
				logger:    logger,
			}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
				"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
			if _, err := rt.RoundTrip(req); err == nil {
				t.Fatal("RoundTrip err = nil, want the token error")
			}
			out := buf.String()
			if out == "" {
				t.Fatal("no log output captured; test proves nothing")
			}
			if strings.Contains(out, tc.marker) {
				t.Errorf("response body leaked into the retry log: %s", out)
			}
			if strings.Contains(out, "S3CR3T-CLIENT-SECRET") {
				t.Errorf("token URL userinfo leaked into the retry log: %s", out)
			}
			for _, key := range []string{
				`"attempt":1`, `"sleep"`, `"error_retryable":true`,
				`"error_class":"oauth_error"`, `"error_status":503`,
			} {
				if !strings.Contains(out, key) {
					t.Errorf("DEBUG retry log lost %s: %s", key, out)
				}
			}
		})
	}
}
