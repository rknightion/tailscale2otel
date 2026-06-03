package tsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
)

// LogStreamConfig is the log-streaming sink configuration PUT to Tailscale.
type LogStreamConfig struct {
	DestinationType string `json:"destinationType"` // e.g. "splunk"
	URL             string `json:"url"`
	Token           string `json:"token,omitempty"`
}

// ConfigureLogStream PUTs cfg as the log-streaming configuration for logType
// ("network" | "configuration"). Returns an error for an invalid logType
// (without contacting the server) or a non-2xx response.
func (c *Client) ConfigureLogStream(ctx context.Context, logType string, cfg LogStreamConfig) error {
	switch logType {
	case "network", "configuration":
	default:
		return fmt.Errorf("tsapi: invalid logType %q (want \"network\" or \"configuration\")", logType)
	}
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "logging", logType, "stream")
	return c.putJSON(ctx, u.String(), cfg)
}

// putJSON marshals body to JSON and PUTs it to urlStr through c.http (so auth,
// retry and the observer transport apply). It returns an error on a non-2xx
// response, including the status and a limited body snippet.
func (c *Client) putJSON(ctx context.Context, urlStr string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, urlStr, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return fmt.Errorf("tsapi: PUT %s: status %d %s: %s", urlStr, resp.StatusCode, http.StatusText(resp.StatusCode), string(snippet))
	}
	return nil
}
