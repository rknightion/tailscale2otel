package tsapi

import (
	"errors"
	"net/http"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"
)

// buildHTTPClient constructs an authenticated, retrying HTTP client from opts.
// OAuth (client-credentials) is preferred; API key is the fallback.
func buildHTTPClient(opts Options) (*http.Client, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
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
	case opts.OAuthClientID != "":
		// OAuthConfig.HTTPClient returns a client whose transport refreshes
		// tokens; wrap it so retries apply to API calls too.
		oc := tsclient.OAuthConfig{
			ClientID:     opts.OAuthClientID,
			ClientSecret: opts.OAuthClientSecret,
			Scopes:       opts.OAuthScopes,
			BaseURL:      opts.BaseURL,
		}.HTTPClient()
		// No whole-client timeout: it would bound the entire retry chain incl.
		// backoff sleeps, so a long Retry-After could never be honored. The
		// retryTransport applies attemptTimeout per attempt instead.
		oc.Timeout = 0
		oc.Transport = wrap(oc.Transport)
		return oc, nil
	case opts.APIKey != "":
		return &http.Client{
			Timeout:   0, // per-attempt timeout lives in retryTransport (see OAuth path)
			Transport: wrap(&authKeyTransport{base: http.DefaultTransport, key: opts.APIKey}),
		}, nil
	default:
		return nil, errors.New("tsapi: no authentication configured (set APIKey or OAuth client credentials)")
	}
}

func orDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
