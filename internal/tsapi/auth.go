package tsapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// defaultTailscaleBaseURL mirrors tsclient's default when Options.BaseURL is unset.
const defaultTailscaleBaseURL = "https://api.tailscale.com"

// buildHTTPClient constructs an authenticated, retrying HTTP client from opts.
// OAuth (client-credentials) and workload identity federation are both
// preferred over a static API key for long-running use (auto-refreshing, no
// fixed expiry); API key is the fallback.
func buildHTTPClient(opts Options) (*http.Client, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultTailscaleBaseURL
	}
	// wrap builds the retrying transport around base. The rate limiter lives ON
	// the retryTransport (not as a wrapping base) so its token wait happens on the
	// parent request context, before the per-attempt HTTP timeout is applied — a
	// long queue wait must not be charged against that timeout. Every attempt
	// (including retries) still acquires its own token.
	wrap := func(base http.RoundTripper) http.RoundTripper {
		return &retryTransport{
			base:           base,
			limiter:        newRateWaiter(opts.RateLimit),
			max:            max(opts.MaxAttempts, 1),
			baseDelay:      orDuration(opts.BaseDelay, 500*time.Millisecond),
			maxDelay:       orDuration(opts.MaxDelay, 10*time.Second),
			attemptTimeout: timeout,
			onRequest:      opts.OnRequest,
			logger:         opts.Logger,
			tracer:         opts.Tracer,
		}
	}

	switch {
	case opts.WorkloadIdentityClientID != "":
		// Same #84 rationale as the OAuth case below: bind the token EXCHANGE to a
		// bounded client via context, since oauth2.Transport ignores the request
		// context when fetching a token.
		src := oauth2.ReuseTokenSource(nil, &workloadIdentityTokenSource{
			ctx:         context.WithValue(context.Background(), oauth2.HTTPClient, newBoundedTokenFetchClient(timeout)),
			baseURL:     baseURL,
			clientID:    opts.WorkloadIdentityClientID,
			idTokenFile: opts.WorkloadIdentityIDTokenFile,
		})
		return &http.Client{
			Timeout:   0,
			Transport: wrap(&oauth2.Transport{Source: src, Base: http.DefaultTransport}),
		}, nil
	case opts.OAuthClientID != "":
		// Build the client-credentials source ourselves rather than via
		// tsclient.OAuthConfig.HTTPClient, which binds the token source to
		// context.Background() on http.DefaultClient (no deadline). oauth2.Transport
		// calls Source.Token() BEFORE the base RoundTrip and IGNORES the request
		// context, so neither http.Client.Timeout nor the per-attempt deadline can
		// bound a token fetch — a hung refresh would block every collector on that
		// tailnet forever via the shared ReuseTokenSource mutex (#84). Binding the
		// source to a context whose oauth2.HTTPClient has dial/TLS/response-header
		// timeouts is the only thing that bounds it.
		cc := clientcredentials.Config{
			ClientID:     opts.OAuthClientID,
			ClientSecret: opts.OAuthClientSecret,
			Scopes:       opts.OAuthScopes,
			TokenURL:     baseURL + "/api/v2/oauth/token",
		}
		src := cc.TokenSource(context.WithValue(context.Background(), oauth2.HTTPClient, newBoundedTokenFetchClient(timeout)))
		// API calls use the default transport (unchanged behavior); only the token
		// FETCH runs on the bounded tokenFetch client above. Wrap so retries apply
		// to API calls too. No whole-client timeout — it would bound the whole retry
		// chain incl. backoff; retryTransport applies attemptTimeout per attempt.
		return &http.Client{
			Timeout:   0,
			Transport: wrap(&oauth2.Transport{Source: src, Base: http.DefaultTransport}),
		}, nil
	case opts.APIKey != "":
		return &http.Client{
			Timeout:   0, // per-attempt timeout lives in retryTransport (see OAuth path)
			Transport: wrap(&authKeyTransport{base: http.DefaultTransport, key: opts.APIKey}),
		}, nil
	default:
		return nil, errors.New("tsapi: no authentication configured (set APIKey, OAuth client credentials, or workload identity)")
	}
}

// newBoundedTokenFetchClient builds an http.Client used ONLY for token-endpoint
// fetches (OAuth client-credentials refresh, or workload-identity token
// exchange). See the #84 rationale on the call sites above.
//
// #200: the Transport-level dial/TLS/response-header timeouts bound
// everything UP TO the arrival of response headers, but not the body read
// that follows — a token endpoint that sends valid headers and then stalls
// mid-body (e.g. a slow/broken chunked response) could still hang the
// refresh forever, serializing every caller behind oauth2.ReuseTokenSource's
// mutex. Client.Timeout bounds the WHOLE exchange (connect + TLS + headers +
// body read), so it is set here too, in addition to the Transport timeouts.
// This is a single unretried call with no backoff, so — unlike
// tailscale.http.timeout, which bounds one attempt inside a retry chain, and
// unlike retryTransport's attemptTimeout, which excludes queued/backoff wait
// time — this bound is the entire token fetch, start to finish.
func newBoundedTokenFetchClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: timeout}).DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			ForceAttemptHTTP2:     true,
		},
	}
}

func orDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
