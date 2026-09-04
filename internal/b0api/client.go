// Package b0api is the read-only Border0 PAM API client.
//
// Border0's PAM API is separate from api.tailscale.com and has no published
// OpenAPI description. This package therefore keeps the wire types and
// response-envelope handling in one small client so collectors do not each
// grow a slightly different interpretation of the captured API.
package b0api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/httpguard"
	"github.com/rknightion/tailscale2otel/v5/internal/jsonbudget"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

const (
	defaultBaseURL         = "https://api.border0.com/api/v1"
	defaultMaxResponseSize = 4 << 20
	defaultTimeout         = 30 * time.Second
	maxStatusBodyBytes     = 1 << 14
)

// Options configures a Client.
//
// Token is a static Border0 service-account bearer token. Border0 service
// account tokens do not use the OAuth refresh flow; this client never refreshes
// or exchanges it. HTTPClient is primarily useful for tests. When it is set,
// the client is cloned so the caller's redirect policy is not changed.
type Options struct {
	BaseURL string // default https://api.border0.com/api/v1
	Token   string // static bearer token

	// BearerToken and APIKey are compatibility spellings for callers that use
	// the naming from another HTTP client. Token takes precedence when more than
	// one is supplied; all three represent the same static bearer credential.
	BearerToken string
	APIKey      string

	UserAgent string
	Timeout   time.Duration

	// MaxResponseBytes bounds a successful JSON response before it is decoded.
	// Zero uses the client's bounded default.
	MaxResponseBytes int64

	// HTTPClient, when non-nil, supplies the underlying transport. It is cloned
	// and wrapped for bearer injection without mutating the caller's client.
	HTTPClient *http.Client
}

// Client talks to the Border0 PAM API using GET requests only.
type Client struct {
	http      *http.Client
	baseURL   *url.URL
	userAgent string

	maxResponseBytes int64
}

// StatusError is the shared typed API error used by apistate.Classify. In
// particular, callers must pass its 403 through apistate with the default
// Disposition{} so a missing Border0 scope is reported as scope_denied rather
// than as a disabled feature.
type StatusError = tsapi.StatusError

// StatusCode returns an HTTP status carried by err, if any.
func StatusCode(err error) (int, bool) { return tsapi.StatusCode(err) }

// ErrResponseTooLarge is returned when a successful response exceeds the
// configured decode budget.
var ErrResponseTooLarge = jsonbudget.ErrTooLarge

// NewClient builds a Border0 client. BaseURL is the API root; when a caller
// supplies only an origin (as httptest servers commonly do), /api/v1 is added
// so custom test endpoints and the default endpoint have identical semantics.
func NewClient(opts Options) (*Client, error) {
	rawBaseURL := opts.BaseURL
	if rawBaseURL == "" {
		rawBaseURL = defaultBaseURL
	}
	baseURL, err := normalizeBaseURL(rawBaseURL)
	if err != nil {
		return nil, err
	}

	token := opts.Token
	if token == "" {
		token = opts.BearerToken
	}
	if token == "" {
		token = opts.APIKey
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("b0api: bearer token is required")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxResponseBytes := opts.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseSize
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	} else {
		// Keep the test/caller transport and timeout, but do not let a redirect
		// replay the bearer credential to another origin.
		httpClient = httpguard.NoRedirectClient(httpClient)
	}
	if opts.HTTPClient == nil {
		// The default client above has no redirect policy until this point. Use
		// the same no-redirect rule for both default and injected clients.
		httpClient = httpguard.NoRedirectClient(httpClient)
	}
	if token != "" {
		baseTransport := httpClient.Transport
		if baseTransport == nil {
			baseTransport = http.DefaultTransport
		}
		httpClient.Transport = &bearerTransport{
			base:    baseTransport,
			token:   token,
			baseURL: baseURL,
		}
	}

	return &Client{
		http:             httpClient,
		baseURL:          baseURL,
		userAgent:        opts.UserAgent,
		maxResponseBytes: maxResponseBytes,
	}, nil
}

func normalizeBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("b0api: invalid BaseURL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("b0api: BaseURL %q must be an absolute HTTP(S) URL", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("b0api: BaseURL %q must not contain credentials, a query, or a fragment", raw)
	}
	if port := u.Port(); port != "" {
		value, parseErr := strconv.Atoi(port)
		if parseErr != nil || value < 1 || value > 65535 {
			return nil, fmt.Errorf("b0api: BaseURL %q has an invalid port", raw)
		}
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/api/v1"
	} else {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	// The API root is intentionally a literal path. Rebuild RawPath from Path
	// in endpointURL so an escaped resource identifier cannot be collapsed by
	// path.Join.
	u.RawPath = ""
	return u, nil
}

// bearerTransport attaches the static token only to requests whose scheme and
// authority match the configured Border0 API origin. NoRedirectClient is the
// first line of redirect defense; this origin check also protects callers that
// supply a transport with its own redirect behavior.
type bearerTransport struct {
	base    http.RoundTripper
	token   string
	baseURL *url.URL
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || !sameOrigin(req.URL, t.baseURL) {
		return t.base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (c *Client) endpointURL(segments ...string) string {
	u := *c.baseURL
	decodedPath := strings.TrimRight(c.baseURL.Path, "/")
	escapedPath := strings.TrimRight(c.baseURL.EscapedPath(), "/")
	for _, segment := range segments {
		decodedPath += "/" + segment
		escapedPath += "/" + url.PathEscape(segment)
	}
	u.Path = decodedPath
	u.RawPath = escapedPath
	u.RawQuery = ""
	return u.String()
}

func (c *Client) endpointURLWithPage(segments []string, options []PageOptions) (string, error) {
	endpoint := c.endpointURL(segments...)
	if len(options) == 0 {
		return endpoint, nil
	}
	if len(options) != 1 {
		return "", errors.New("b0api: at most one page option is allowed")
	}
	normalized, err := options[0].normalized()
	if err != nil {
		return "", err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("page", fmt.Sprint(normalized.Page))
	q.Set("page_size", fmt.Sprint(normalized.PageSize))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func invalidPageOption(name string, value int) error {
	return fmt.Errorf("b0api: %s must not be negative (got %d)", name, value)
}

// getJSON performs one authenticated GET and decodes a successful JSON body.
// It never retries or refreshes the static bearer token; callers can classify
// the typed HTTP status error through apistate.Classify.
func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxStatusBodyBytes))
		return &tsapi.StatusError{
			Method: http.MethodGet,
			URL:    endpoint,
			Code:   resp.StatusCode,
			Body:   string(body),
		}
	}

	budget := jsonbudget.Of("b0api", c.maxResponseBytes, "")
	if resp.ContentLength > budget.MaxBytes {
		return budget.ByteCeilingError()
	}
	return jsonbudget.Decode(resp.Body, budget, out)
}

// Organization returns the organization configuration.
func (c *Client) Organization(ctx context.Context) (*Organization, error) {
	var out Organization
	if err := c.getJSON(ctx, c.endpointURL("organization"), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ServerInfo returns Border0's server consistency information.
func (c *Client) ServerInfo(ctx context.Context) (*ServerInfo, error) {
	var out ServerInfo
	if err := c.getJSON(ctx, c.endpointURL("serverinfo"), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Connectors lists all connectors. This endpoint returns {list} without a
// pagination object.
func (c *Client) Connectors(ctx context.Context) ([]Connector, error) {
	var wire struct {
		List []Connector `json:"list"`
	}
	if err := c.getJSON(ctx, c.endpointURL("connectors"), &wire); err != nil {
		return nil, err
	}
	return wire.List, nil
}

// Connector returns one connector by ID.
func (c *Client) Connector(ctx context.Context, connectorID string) (*Connector, error) {
	var out Connector
	if err := c.getJSON(ctx, c.endpointURL("connector", connectorID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ConnectorTokens returns connector token metadata. Border0 does not return
// token values from this endpoint.
func (c *Client) ConnectorTokens(ctx context.Context, connectorID string) ([]ConnectorToken, error) {
	var wire struct {
		Connector ConnectorRef     `json:"connector"`
		List      []ConnectorToken `json:"list"`
	}
	if err := c.getJSON(ctx, c.endpointURL("connector", connectorID, "tokens"), &wire); err != nil {
		return nil, err
	}
	return wire.List, nil
}

// ConnectorPlugins returns the connector's plugin list. Plugin is a raw keyed
// object because Border0 publishes no plugin schema.
func (c *Client) ConnectorPlugins(ctx context.Context, connectorID string) ([]Plugin, error) {
	var wire struct {
		Connector ConnectorRef `json:"connector"`
		List      []Plugin     `json:"list"`
	}
	if err := c.getJSON(ctx, c.endpointURL("connector", connectorID, "plugins"), &wire); err != nil {
		return nil, err
	}
	return wire.List, nil
}

// Sockets lists all PAM services, flattening the paginated envelope to match
// the ordinary list methods in internal/tsapi.
func (c *Client) Sockets(ctx context.Context) ([]Socket, error) {
	page, err := c.SocketsPage(ctx)
	if err != nil {
		return nil, err
	}
	return page.List, nil
}

// SocketsPage preserves the pagination envelope for callers that need to walk
// more than one page.
func (c *Client) SocketsPage(ctx context.Context, options ...PageOptions) (SocketPage, error) {
	var wire SocketPage
	endpoint, err := c.endpointURLWithPage([]string{"sockets"}, options)
	if err != nil {
		return wire, err
	}
	if err := c.getJSON(ctx, endpoint, &wire); err != nil {
		return wire, err
	}
	return wire, nil
}

// Socket returns one PAM service by ID.
func (c *Client) Socket(ctx context.Context, socketID string) (*Socket, error) {
	var out Socket
	if err := c.getJSON(ctx, c.endpointURL("socket", socketID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SocketConnectors returns the connector links for a PAM service.
func (c *Client) SocketConnectors(ctx context.Context, socketID string) ([]SocketConnectorLink, error) {
	var wire struct {
		List []SocketConnectorLink `json:"list"`
	}
	if err := c.getJSON(ctx, c.endpointURL("socket", socketID, "connectors"), &wire); err != nil {
		return nil, err
	}
	return wire.List, nil
}

// SocketUpstreamConfigurations returns the service's upstream configurations.
// The response can include cleartext credentials; callers must not serialize
// the returned values into telemetry or logs.
func (c *Client) SocketUpstreamConfigurations(ctx context.Context, socketID string) ([]UpstreamConfiguration, error) {
	var wire struct {
		List []UpstreamConfiguration `json:"list"`
	}
	if err := c.getJSON(ctx, c.endpointURL("socket", socketID, "upstream_configurations"), &wire); err != nil {
		return nil, err
	}
	return wire.List, nil
}

// Policies lists policies from the bare JSON array returned by Border0.
func (c *Client) Policies(ctx context.Context) ([]Policy, error) {
	var out []Policy
	if err := c.getJSON(ctx, c.endpointURL("policies"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Policy returns one policy by ID.
func (c *Client) Policy(ctx context.Context, policyID string) (*Policy, error) {
	var out Policy
	if err := c.getJSON(ctx, c.endpointURL("policy", policyID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IAMUsers lists IAM users, flattening the paginated envelope.
func (c *Client) IAMUsers(ctx context.Context) ([]IAMUser, error) {
	page, err := c.IAMUsersPage(ctx)
	if err != nil {
		return nil, err
	}
	return page.List, nil
}

// IAMUsersPage preserves the IAM users pagination envelope.
func (c *Client) IAMUsersPage(ctx context.Context, options ...PageOptions) (IAMUserPage, error) {
	var wire IAMUserPage
	endpoint, err := c.endpointURLWithPage([]string{"organizations", "iam", "users"}, options)
	if err != nil {
		return wire, err
	}
	if err := c.getJSON(ctx, endpoint, &wire); err != nil {
		return wire, err
	}
	return wire, nil
}

// IAMGroups lists IAM groups, flattening the paginated envelope.
func (c *Client) IAMGroups(ctx context.Context) ([]IAMGroup, error) {
	page, err := c.IAMGroupsPage(ctx)
	if err != nil {
		return nil, err
	}
	return page.List, nil
}

// IAMGroupsPage preserves the IAM groups pagination envelope.
func (c *Client) IAMGroupsPage(ctx context.Context, options ...PageOptions) (IAMGroupPage, error) {
	var wire IAMGroupPage
	endpoint, err := c.endpointURLWithPage([]string{"organizations", "iam", "groups"}, options)
	if err != nil {
		return wire, err
	}
	if err := c.getJSON(ctx, endpoint, &wire); err != nil {
		return wire, err
	}
	return wire, nil
}

// IAMServiceAccounts lists IAM service accounts, flattening the paginated
// envelope. Callers should split the resulting records by Role because PAM
// mirrors tags as client-role service accounts.
func (c *Client) IAMServiceAccounts(ctx context.Context) ([]ServiceAccount, error) {
	page, err := c.IAMServiceAccountsPage(ctx)
	if err != nil {
		return nil, err
	}
	return page.List, nil
}

// IAMServiceAccountsPage preserves the service-account pagination envelope.
func (c *Client) IAMServiceAccountsPage(ctx context.Context, options ...PageOptions) (ServiceAccountPage, error) {
	var wire ServiceAccountPage
	endpoint, err := c.endpointURLWithPage([]string{"organizations", "iam", "service_accounts"}, options)
	if err != nil {
		return wire, err
	}
	if err := c.getJSON(ctx, endpoint, &wire); err != nil {
		return wire, err
	}
	return wire, nil
}

// ServiceAccountTokens returns token metadata for a named service account.
func (c *Client) ServiceAccountTokens(ctx context.Context, name string) ([]ServiceAccountToken, error) {
	var wire struct {
		Items int                   `json:"items"`
		List  []ServiceAccountToken `json:"list"`
	}
	if err := c.getJSON(ctx, c.endpointURL("organizations", "iam", "service_accounts", name, "tokens"), &wire); err != nil {
		return nil, err
	}
	return wire.List, nil
}

// Sessions lists organization-wide session logs. Border0 ignores all filters
// other than page and page_size, so only those options are accepted.
func (c *Client) Sessions(ctx context.Context, options ...PageOptions) (SessionPage, error) {
	var wire SessionPage
	endpoint, err := c.endpointURLWithPage([]string{"sessions"}, options)
	if err != nil {
		return wire, err
	}
	if err := c.getJSON(ctx, endpoint, &wire); err != nil {
		return wire, err
	}
	return wire, nil
}

// SessionLogs is an alias with the response's wire terminology.
func (c *Client) SessionLogs(ctx context.Context, options ...PageOptions) (SessionPage, error) {
	return c.Sessions(ctx, options...)
}

// SocketSessions lists session logs scoped to one PAM service. A zero-session
// response is the literal JSON object {}, which decodes to a SessionPage with
// nil Pagination and no SessionLogs.
func (c *Client) SocketSessions(ctx context.Context, socketID string, options ...PageOptions) (SessionPage, error) {
	var wire SessionPage
	endpoint, err := c.endpointURLWithPage([]string{"socket", socketID, "sessions"}, options)
	if err != nil {
		return wire, err
	}
	if err := c.getJSON(ctx, endpoint, &wire); err != nil {
		return wire, err
	}
	return wire, nil
}

// SocketSessionLogs is an alias with the response's wire terminology.
func (c *Client) SocketSessionLogs(ctx context.Context, socketID string, options ...PageOptions) (SessionPage, error) {
	return c.SocketSessions(ctx, socketID, options...)
}

// SessionsPage is a convenience for callers that prefer positional pagination
// arguments while retaining the same response shape.
func (c *Client) SessionsPage(ctx context.Context, page, pageSize int) (SessionPage, error) {
	return c.Sessions(ctx, PageOptions{Page: page, PageSize: pageSize})
}

// SocketSessionsPage is the positional-argument counterpart to SocketSessions.
func (c *Client) SocketSessionsPage(ctx context.Context, socketID string, page, pageSize int) (SessionPage, error) {
	return c.SocketSessions(ctx, socketID, PageOptions{Page: page, PageSize: pageSize})
}

// ConnectorRef is the small connector envelope echoed by token/plugin calls.
type ConnectorRef struct {
	ConnectorID string `json:"connector_id"`
	Name        string `json:"name"`
}
