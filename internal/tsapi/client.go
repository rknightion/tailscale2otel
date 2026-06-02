// Package tsapi wraps the Tailscale API: the official tsclient for snapshot
// resources (devices, users, keys, DNS, ACL, settings, webhooks) plus a thin
// custom doer for the two log-polling endpoints the client does not cover.
// Both share one authenticated, retrying *http.Client.
package tsapi

import (
	"context"
	"errors"
	"fmt"
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

// Keys lists all auth/API keys visible to the principal.
func (c *Client) Keys(ctx context.Context) ([]tsclient.Key, error) {
	return c.ts.Keys().List(ctx, true)
}

// TailnetSettings returns the tailnet feature settings.
func (c *Client) TailnetSettings(ctx context.Context) (*tsclient.TailnetSettings, error) {
	return c.ts.TailnetSettings().Get(ctx)
}

// Webhooks lists configured webhook endpoints.
func (c *Client) Webhooks(ctx context.Context) ([]tsclient.Webhook, error) {
	return c.ts.Webhooks().List(ctx)
}

// PolicyFileRaw returns the raw HuJSON ACL policy and its ETag.
func (c *Client) PolicyFileRaw(ctx context.Context) (*tsclient.RawACL, error) {
	return c.ts.PolicyFile().Raw(ctx)
}

// DNSNameservers returns the configured global nameservers.
func (c *Client) DNSNameservers(ctx context.Context) ([]string, error) {
	return c.ts.DNS().Nameservers(ctx)
}

// DNSSearchPaths returns the configured DNS search paths.
func (c *Client) DNSSearchPaths(ctx context.Context) ([]string, error) {
	return c.ts.DNS().SearchPaths(ctx)
}

// DNSPreferences returns the MagicDNS preferences.
func (c *Client) DNSPreferences(ctx context.Context) (*tsclient.DNSPreferences, error) {
	return c.ts.DNS().Preferences(ctx)
}

// DNSSplitDNS returns the split-DNS configuration.
func (c *Client) DNSSplitDNS(ctx context.Context) (tsclient.SplitDNSResponse, error) {
	return c.ts.DNS().SplitDNS(ctx)
}
