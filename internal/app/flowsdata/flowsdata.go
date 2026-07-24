// Package flowsdata is the wire and template contract for the built-in flow
// view: Response is what /api/flows.json returns, Page is the server-rendered
// shell of /flows. It is a leaf package so the HTTP handler and the template
// package can share the shapes without either importing the other.
package flowsdata

import "github.com/rknightion/tailscale2otel/v2/internal/flowstore"

// Response is one answer from /api/flows.json. Everything the page draws comes
// from here, so the page can be a static shell that polls.
type Response struct {
	// Tailnet is the tailnet this answer describes; Tailnets lists every one the
	// process observes, so the page can offer a selector without a second call.
	Tailnet  string   `json:"tailnet"`
	Tailnets []string `json:"tailnets"`
	// Window is the requested lookback (already clamped to Retention); the span
	// actually covered by retained data is Result.Window.
	Window    string `json:"window"`
	Retention string `json:"retention"`
	// Stats describes the store itself — how much it holds and how much it had to
	// fold away — so the page can be honest about coverage.
	Stats  flowstore.Stats  `json:"stats"`
	Result flowstore.Result `json:"result"`
	// Recent is the newest raw connections, newest first. Always an array, never
	// null: the page iterates it unconditionally.
	Recent      []flowstore.Recent `json:"recent"`
	GeneratedAt string             `json:"generated_at"`
}

// Page is the server-rendered shell of /flows. It carries only what must exist
// before the first poll completes — identity, the tailnet list, and the poll
// cadence. All traffic data arrives from Response.
type Page struct {
	ServiceName string
	Version     string
	Tailnets    []string
	Tailnet     string
	Retention   string
	// RefreshMs is how often the page re-polls /api/flows.json, from
	// admin.status_refresh_interval.
	RefreshMs int
}
