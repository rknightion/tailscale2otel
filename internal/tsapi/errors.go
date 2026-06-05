package tsapi

import "fmt"

// StatusError is returned by the JSON helpers when the Tailscale API responds
// with a non-2xx HTTP status. Callers can errors.As it to branch on the HTTP
// status code — e.g. the logstream collector treats a 4xx on the stream-status
// endpoint as "feature not configured" rather than a scrape failure.
type StatusError struct {
	Method string
	URL    string
	Code   int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("tsapi: %s %s: status %d: %s", e.Method, e.URL, e.Code, e.Body)
}
