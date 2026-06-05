package tsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"time"
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

// LogStreamStatus is the delivery-health status of a configured log stream, from
// GET /api/v2/tailnet/{tailnet}/logging/{logType}/stream/status. Only the fields
// tailscale2otel emits are decoded; the rate*/nanosecond fields on the wire are
// intentionally ignored (rates are derivable via PromQL on the counters).
type LogStreamStatus struct {
	LastActivity       time.Time `json:"lastActivity"`
	LastError          string    `json:"lastError"`
	MaxBodySize        int64     `json:"maxBodySize"`
	MaxNumEntries      int64     `json:"maxNumEntries"`
	NumSpoofedEntries  int64     `json:"numSpoofedEntries"`
	NumBytesSent       int64     `json:"numBytesSent"`
	NumEntriesSent     int64     `json:"numEntriesSent"`
	NumFailedRequests  int64     `json:"numFailedRequests"`
	NumTotalRequests   int64     `json:"numTotalRequests"`
	NumMaxBodyRequests int64     `json:"numMaxBodyRequests"`
}

// LogStreamStatus fetches the delivery-health status of the configured log
// stream for logType ("network" | "configuration"). A non-2xx response is
// returned as a *StatusError; a 4xx typically means no stream is configured for
// that log type.
func (c *Client) LogStreamStatus(ctx context.Context, logType string) (*LogStreamStatus, error) {
	switch logType {
	case "network", "configuration":
	default:
		return nil, fmt.Errorf("tsapi: invalid logType %q (want \"network\" or \"configuration\")", logType)
	}
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "logging", logType, "stream", "status")
	var out LogStreamStatus
	if err := c.getJSON(ctx, u.String(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
