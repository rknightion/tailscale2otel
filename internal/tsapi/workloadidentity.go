package tsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
)

const (
	workloadIdentityExchangePath    = "/api/v2/oauth/token-exchange"
	maxWorkloadIdentityExchangeBody = 64 << 10
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
// Redirect handling (#467): the exchange POSTs the JWT in a form body, and
// under Go's default redirect policy a cross-origin 307/308 replays that body —
// JWT included — to whatever origin the response names. The bounded client in
// ctx therefore carries the shared redirectPolicy: a redirect is followed only
// when its target is the exact same origin (scheme, host, port, and no injected
// userinfo) the exchange started on, so a same-origin move of the endpoint
// keeps working while any other target is refused BEFORE the request is sent.
// A refusal surfaces from Token() as a *CrossOriginRedirectError (matching
// ErrCrossOriginRedirect, diagnostic class "redirect_refused"), wrapped in the
// "workload identity token exchange failed" error below; it names the two
// origins only, never the JWT, the body or the full destination URL.
type workloadIdentityTokenSource struct {
	// ctx carries the bounded token-fetch HTTP client (oauth2.HTTPClient) so the
	// exchange request is subject to the same dial/TLS/response-header timeouts
	// as the OAuth client-credentials path (#84) rather than blocking forever,
	// and to the shared cross-origin redirect refusal described above (#467).
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

	form := url.Values{
		"client_id": {s.clientID},
		"jwt":       {idToken},
	}
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost,
		strings.TrimRight(s.baseURL, "/")+workloadIdentityExchangePath,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("tsapi: building workload identity token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := http.DefaultClient
	if configured, ok := s.ctx.Value(oauth2.HTTPClient).(*http.Client); ok && configured != nil {
		client = configured
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tsapi: workload identity token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWorkloadIdentityExchangeBody+1))
	if err != nil {
		return nil, fmt.Errorf("tsapi: reading workload identity token exchange response: %w", err)
	}
	if len(body) > maxWorkloadIdentityExchangeBody {
		return nil, fmt.Errorf("tsapi: workload identity token exchange response exceeds %d bytes", maxWorkloadIdentityExchangeBody)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, workloadIdentityExchangeHTTPError(resp.StatusCode, body, idToken)
	}

	var exchanged struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &exchanged); err != nil {
		return nil, fmt.Errorf("tsapi: decoding workload identity token exchange response: %w", err)
	}
	if exchanged.AccessToken == "" {
		return nil, fmt.Errorf("tsapi: workload identity token exchange response has no access_token")
	}

	tok := &oauth2.Token{AccessToken: exchanged.AccessToken, TokenType: exchanged.TokenType}
	if exchanged.ExpiresIn > 0 {
		tok.Expiry = time.Now().Add(time.Duration(exchanged.ExpiresIn) * time.Second)
	}
	return tok, nil
}

// workloadIdentityExchangeHTTPError returns a stable, credential-free error
// for a rejected exchange. Tailscale's documented 401 response carries a
// message field with the operator-facing diagnostic. Other response fields and
// raw bodies are deliberately not surfaced: the submitted JWT must never be
// echoed into logs by an intermediary or malformed error response.
func workloadIdentityExchangeHTTPError(status int, body []byte, submittedJWT string) error {
	var response struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &response); err == nil {
		message := response.Message
		if submittedJWT != "" {
			message = strings.ReplaceAll(message, submittedJWT, "[redacted]")
		}
		message = safeDiagnosticText(message)
		if message != "" {
			return newWorkloadIdentityExchangeError(status, message)
		}
	}
	return newWorkloadIdentityExchangeError(status, http.StatusText(status))
}

// workloadIdentityExchangeError keeps the stable workload-identity diagnostic
// while unwrapping to a body-free oauth2.RetrieveError. The shared retry and
// logging transport already understands that type: 401/403 stay terminal,
// status is recorded separately, and the raw response body never reaches logs.
type workloadIdentityExchangeError struct {
	status   int
	message  string
	retrieve *oauth2.RetrieveError
}

func newWorkloadIdentityExchangeError(status int, message string) *workloadIdentityExchangeError {
	return &workloadIdentityExchangeError{
		status:  status,
		message: message,
		retrieve: &oauth2.RetrieveError{
			Response: &http.Response{
				StatusCode: status,
				Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			},
			ErrorCode:        "workload_identity_exchange",
			ErrorDescription: message,
		},
	}
}

func (e *workloadIdentityExchangeError) Error() string {
	return fmt.Sprintf("tsapi: workload identity token exchange failed: HTTP %d: %s", e.status, e.message)
}

func (e *workloadIdentityExchangeError) Unwrap() error { return e.retrieve }

// readIDTokenFile reads and trims the OIDC ID token at path, naming the path
// in the returned error so a missing or unreadable projected token is
// diagnosable from the surfaced request error.
func readIDTokenFile(path string) (string, error) {
	b, err := safefile.ReadRegular(path, safefile.MaxSecretBytes, safefile.AllowSymlink)
	if err != nil {
		return "", fmt.Errorf("tsapi: reading workload identity ID token file %q: %w", path, err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("tsapi: workload identity ID token file %q is empty", path)
	}
	return token, nil
}
