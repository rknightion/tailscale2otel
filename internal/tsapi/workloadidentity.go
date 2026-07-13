package tsapi

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/oauth2"
)

// workloadIdentityTokenSource exchanges a workload's OIDC ID token for a
// short-lived Tailscale API access token via POST
// /api/v2/oauth/token-exchange (form fields client_id + jwt; no scope
// parameter — the exchanged token's scopes are fixed by the federated
// identity's configuration in the Tailscale admin console, not requested
// here). This mirrors the exchange tailscale/tailscale's own
// feature/identityfederation package performs internally; tailscale-client-go
// v2 (the client this package otherwise wraps) has no workload-identity
// support to call into.
//
// The ID token is re-read from idTokenFile on every Token() call — Kubernetes
// projected service-account tokens rotate in place, and caching the first
// read would eventually submit an expired JWT to the exchange.
type workloadIdentityTokenSource struct {
	// ctx carries the bounded token-fetch HTTP client (oauth2.HTTPClient) so the
	// exchange request is subject to the same dial/TLS/response-header timeouts
	// as the OAuth client-credentials path (#84) rather than blocking forever.
	ctx         context.Context
	baseURL     string
	clientID    string
	idTokenFile string
}

func (s *workloadIdentityTokenSource) Token() (*oauth2.Token, error) {
	idToken, err := readIDTokenFile(s.idTokenFile)
	if err != nil {
		return nil, err
	}
	cfg := &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: s.baseURL + "/api/v2/oauth/token-exchange"}}
	tok, err := cfg.Exchange(s.ctx, "",
		oauth2.SetAuthURLParam("client_id", s.clientID),
		oauth2.SetAuthURLParam("jwt", idToken))
	if err != nil {
		return nil, fmt.Errorf("tsapi: workload identity token exchange failed: %w", err)
	}
	return tok, nil
}

// readIDTokenFile reads and trims the OIDC ID token at path, naming the path
// in the returned error so a missing or unreadable projected token is
// diagnosable from the surfaced request error.
func readIDTokenFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("tsapi: reading workload identity ID token file %q: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}
