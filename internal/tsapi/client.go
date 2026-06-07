// Package tsapi wraps the Tailscale API: the official tsclient for snapshot
// resources (devices, users, DNS, ACL, settings, webhooks, contacts) plus a thin
// custom doer for resources the client does not cover or under-populates (key
// inventory, posture, log polling, and other raw-decode endpoints). Both share
// one authenticated, retrying *http.Client.
package tsapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"
)

const defaultBaseURL = "https://api.tailscale.com"

// Options configures a Client.
type Options struct {
	Tailnet   string
	BaseURL   string // default https://api.tailscale.com
	UserAgent string

	// Authentication (used only when HTTPClient is nil). OAuth is preferred for
	// long-running use (auto-refreshing, no fixed expiry).
	APIKey            string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScopes       []string

	Timeout     time.Duration
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration

	// OnRequest, when non-nil, is invoked once after each logical API request
	// completes with a RequestInfo: a low-cardinality endpoint label (e.g.
	// "devices", "logging/network"), the final HTTP status (0 on transport
	// error), the total attempt count (1 if the first try succeeded), the
	// request's wall-clock duration and any transport error text. For
	// self-observability.
	OnRequest func(RequestInfo)

	// Logger, when non-nil, receives status-aware transport logs (429 retries at
	// INFO, 5xx retries at DEBUG, auth failures at ERROR). Nil disables transport
	// logging.
	Logger *slog.Logger

	// RateLimit caps the request rate, in requests per second, across the whole
	// client. Zero means unlimited (pure pass-through).
	RateLimit float64

	// HTTPClient, when set, is used as-is (tests); auth/retry are not applied.
	HTTPClient *http.Client
}

// Client is the Tailscale API facade used by collectors.
type Client struct {
	ts        *tsclient.Client
	http      *http.Client
	baseURL   *url.URL
	tailnet   string
	userAgent string
}

// NewClient builds a Client from opts.
func NewClient(opts Options) (*Client, error) {
	if opts.Tailnet == "" {
		return nil, errors.New("tsapi: Tailnet is required")
	}
	raw := opts.BaseURL
	if raw == "" {
		raw = defaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("tsapi: invalid BaseURL %q: %w", raw, err)
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient, err = buildHTTPClient(opts)
		if err != nil {
			return nil, err
		}
	}

	ts := &tsclient.Client{
		Tailnet:   opts.Tailnet,
		BaseURL:   u,
		HTTP:      httpClient,
		UserAgent: opts.UserAgent,
	}
	if opts.APIKey != "" {
		ts.APIKey = opts.APIKey
	}

	return &Client{
		ts:        ts,
		http:      httpClient,
		baseURL:   u,
		tailnet:   opts.Tailnet,
		userAgent: opts.UserAgent,
	}, nil
}

// Devices lists all devices in the tailnet.
func (c *Client) Devices(ctx context.Context) ([]tsclient.Device, error) {
	return c.ts.Devices().List(ctx)
}

// Users lists all users in the tailnet.
func (c *Client) Users(ctx context.Context) ([]tsclient.User, error) {
	return c.ts.Users().List(ctx, nil, nil)
}

// Webhooks lists configured webhook endpoints.
func (c *Client) Webhooks(ctx context.Context) ([]tsclient.Webhook, error) {
	return c.ts.Webhooks().List(ctx)
}

// Contacts returns the tailnet's account/support/security contacts.
func (c *Client) Contacts(ctx context.Context) (*tsclient.Contacts, error) {
	return c.ts.Contacts().Get(ctx)
}

// PolicyFileRaw returns the raw HuJSON ACL policy and its ETag.
func (c *Client) PolicyFileRaw(ctx context.Context) (*tsclient.RawACL, error) {
	return c.ts.PolicyFile().Raw(ctx)
}
