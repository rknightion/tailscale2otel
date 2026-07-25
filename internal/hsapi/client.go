// Package hsapi is a minimal read-only HTTP/JSON client for the Headscale
// control-plane API (/api/v1/*), authenticated with a Bearer API key. It mirrors
// the internal/tsapi getJSON + URL-builder pattern but without retry (small
// tailnets; retry is a noted follow-up).
package hsapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/jsonbudget"
)

// maxDrainBytes caps the post-request body drain (see getJSON).
const maxDrainBytes = 64 << 10

// Options configures the Headscale client.
type Options struct {
	URL     string        // control-plane base URL, e.g. https://hs.example.org
	APIKey  string        // Bearer token
	Timeout time.Duration // per-request timeout (0 = no client timeout)

	// MaxResponseBytes bounds a single successful JSON response body before it
	// is decoded. Zero uses defaultMaxResponseBytes. See limit.go for the sizing
	// evidence and the tuning constraint.
	MaxResponseBytes int64
}

// Client talks to a Headscale server.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client

	// maxResponseBytes is the response decode byte budget, resolved once in
	// NewClient (#488).
	maxResponseBytes int64
}

// NewClient builds a Headscale client from opts.
func NewClient(opts Options) *Client {
	maxBytes := opts.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	return &Client{
		baseURL:          strings.TrimRight(opts.URL, "/"),
		apiKey:           opts.APIKey,
		http:             &http.Client{Timeout: opts.Timeout},
		maxResponseBytes: maxBytes,
	}
}

// budget is the decode budget applied to every endpoint. Headscale exposes only
// snapshot resources, so there is a single tier (unlike tsapi's snapshot/log
// split).
func (c *Client) budget() jsonbudget.Budget { return budgetOf(c.maxResponseBytes) }

// getJSON performs an authenticated GET of path and decodes JSON into out.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	// Drain a BOUNDED remainder before closing so the connection stays reusable.
	// The bound is the point: after a budget violation (or a truncated non-200
	// read) the rest of the body may be endless, and an unbounded drain would
	// keep pulling it until the client timeout — 30s of wasted bandwidth per poll
	// on a body already rejected. 64 KiB comfortably covers a real leftover.
	defer func() { _, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes); _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("headscale GET %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	budget := c.budget()
	// Content-Length, when the upstream declares one, lets an over-budget body be
	// rejected before a single byte of it is read. It is advisory (absent on a
	// chunked response, where ContentLength is -1), so the streaming budget below
	// remains the real control.
	if resp.ContentLength > budget.MaxBytes {
		return budget.ByteCeilingError()
	}
	return jsonbudget.Decode(resp.Body, budget, out)
}

// Nodes lists all nodes (GET /api/v1/node).
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var r nodesResponse
	if err := c.getJSON(ctx, "/api/v1/node", &r); err != nil {
		return nil, err
	}
	return r.Nodes, nil
}

// HSUsers lists all users (GET /api/v1/user).
func (c *Client) HSUsers(ctx context.Context) ([]User, error) {
	var r usersResponse
	if err := c.getJSON(ctx, "/api/v1/user", &r); err != nil {
		return nil, err
	}
	return r.Users, nil
}

// PreAuthKeys lists all pre-auth keys (GET /api/v1/preauthkey).
func (c *Client) PreAuthKeys(ctx context.Context) ([]PreAuthKey, error) {
	var r preAuthKeysResponse
	if err := c.getJSON(ctx, "/api/v1/preauthkey", &r); err != nil {
		return nil, err
	}
	return r.PreAuthKeys, nil
}

// APIKeys lists all API keys (GET /api/v1/apikey).
func (c *Client) APIKeys(ctx context.Context) ([]APIKey, error) {
	var r apiKeysResponse
	if err := c.getJSON(ctx, "/api/v1/apikey", &r); err != nil {
		return nil, err
	}
	return r.APIKeys, nil
}

// PolicyDoc fetches the ACL policy document (GET /api/v1/policy).
func (c *Client) PolicyDoc(ctx context.Context) (*Policy, error) {
	var p Policy
	if err := c.getJSON(ctx, "/api/v1/policy", &p); err != nil {
		return nil, err
	}
	return &p, nil
}
