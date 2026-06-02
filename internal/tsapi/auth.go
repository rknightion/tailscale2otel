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
	// wrap composes the transport stack: a rate limiter (outermost, when
	// configured) around the retrying transport around base.
	wrap := func(base http.RoundTripper) http.RoundTripper {
		rt := &retryTransport{
			base:      base,
			max:       max(opts.MaxAttempts, 1),
			baseDelay: orDuration(opts.BaseDelay, 500*time.Millisecond),
			maxDelay:  orDuration(opts.MaxDelay, 10*time.Second),
			onRequest: opts.OnRequest,
		}
		return wrapRateLimit(rt, opts.RateLimit)
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
		oc.Timeout = timeout
		oc.Transport = wrap(oc.Transport)
		return oc, nil
	case opts.APIKey != "":
		return &http.Client{
			Timeout:   timeout,
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
