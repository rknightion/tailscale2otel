package tsapi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/audit"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
)

// NetworkFlowLogs fetches network flow logs for the window [start, end].
func (c *Client) NetworkFlowLogs(ctx context.Context, start, end time.Time) (flowlog.NetworkResponse, error) {
	var out flowlog.NetworkResponse
	err := c.getJSONBudgeted(ctx, c.logURL("network", start, end), c.logBudget(), &out)
	return out, err
}

// ConfigAuditLogs fetches configuration audit logs for the window [start, end].
func (c *Client) ConfigAuditLogs(ctx context.Context, start, end time.Time) (audit.ConfigurationResponse, error) {
	var out audit.ConfigurationResponse
	err := c.getJSONBudgeted(ctx, c.logURL("configuration", start, end), c.logBudget(), &out)
	return out, err
}

func (c *Client) logURL(kind string, start, end time.Time) string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "logging", kind)
	q := url.Values{}
	q.Set("start", start.UTC().Format(time.RFC3339))
	q.Set("end", end.UTC().Format(time.RFC3339))
	u.RawQuery = q.Encode()
	return u.String()
}

// getJSON fetches urlStr and decodes the body under the snapshot-endpoint
// budget (tailscale.max_response_bytes). The bulk log pulls use
// getJSONBudgeted with the larger log budget instead (#474).
func (c *Client) getJSON(ctx context.Context, urlStr string, out any) error {
	return c.getJSONBudgeted(ctx, urlStr, c.apiBudget(), out)
}

func (c *Client) getJSONBudgeted(ctx context.Context, urlStr string, budget decodeBudget, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return &StatusError{Method: http.MethodGet, URL: urlStr, Code: resp.StatusCode, Body: string(body)}
	}
	// Content-Length, when the upstream declares one, lets an over-budget body be
	// rejected before a single byte of it is read. It is advisory (absent on a
	// chunked response, where ContentLength is -1), so the streaming budget below
	// remains the real control.
	if resp.ContentLength > budget.MaxBytes {
		return budget.ByteCeilingError()
	}
	return decodeJSONBudgeted(resp.Body, budget, out)
}
