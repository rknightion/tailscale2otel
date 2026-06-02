package tsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
)

// NetworkFlowLogs fetches network flow logs for the window [start, end].
func (c *Client) NetworkFlowLogs(ctx context.Context, start, end time.Time) (flowlog.NetworkResponse, error) {
	var out flowlog.NetworkResponse
	err := c.getJSON(ctx, c.logURL("network", start, end), &out)
	return out, err
}

// ConfigAuditLogs fetches configuration audit logs for the window [start, end].
func (c *Client) ConfigAuditLogs(ctx context.Context, start, end time.Time) (audit.ConfigurationResponse, error) {
	var out audit.ConfigurationResponse
	err := c.getJSON(ctx, c.logURL("configuration", start, end), &out)
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

func (c *Client) getJSON(ctx context.Context, urlStr string, out any) error {
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
		return fmt.Errorf("tsapi: GET %s: status %d: %s", urlStr, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
