// Package eventsdata is the wire and template contract for the built-in
// audit/webhook event explorer (#300): Response is what /api/events.json
// returns, Page is the server-rendered shell of /events. It is a leaf
// package so the HTTP handler and the template package can share the shapes
// without either importing the other — mirrors internal/app/flowsdata.
package eventsdata

import "github.com/rknightion/tailscale2otel/v4/internal/eventstore"

// SchemaVersion is Response's contract version (#323): a stable integer an
// external consumer can branch on, bumped only for a deliberate breaking
// change. See docs/api/compatibility.md for the additive-vs-breaking policy
// and internal/app/apicontract for how CI enforces it.
const SchemaVersion = 1

// Response is one answer from /api/events.json. Everything the page draws
// comes from here, so the page can be a static shell that polls.
type Response struct {
	// SchemaVersion is the package-level SchemaVersion at build time.
	SchemaVersion int `json:"schema_version"`
	// Stats describes the store itself — how much it holds and how much it had
	// to evict — so the page can be honest about coverage.
	Stats eventstore.Stats `json:"stats"`
	// Rows is the matching page of events, newest first. Always an array,
	// never null: the page iterates it unconditionally.
	Rows []eventstore.Event `json:"rows"`
	// Filters echoes back the server-side filter this response was computed
	// with, so the page can restore its inputs across a reload without a
	// second round trip. omitzero, not omitempty: omitempty is a silent no-op
	// on a struct field, so an unfiltered response would always carry
	// "filters":{}. omitzero drops the key when no filter was applied
	// (mirrors flowsdata.Response.Filters).
	Filters     Filters `json:"filters,omitzero"`
	GeneratedAt string  `json:"generated_at"`
	// Window is the requested lookback (already clamped); a zero-value Start on
	// the request means "everything retained", in which case this reports the
	// empty string rather than a misleading fixed window.
	Window string `json:"window,omitempty"`
	// Matched is how many rows in the requested window satisfy Filters,
	// ignoring pagination — "how many are there", not "how many this page
	// returned".
	Matched int `json:"matched"`
	// Returned is len(Rows) — how many rows this response actually carries,
	// always <= the requested page size.
	Returned int `json:"returned"`
	// Retained is how many rows the ring currently holds, independent of both
	// the window and Filters.
	Retained int `json:"retained"`
	// Truncated reports the ring is at capacity, so a matching row older than
	// what's shown may already have been evicted rather than simply not
	// existing.
	Truncated bool `json:"truncated"`
	// NextCursor resumes Rows past this page, opaque on the wire — see
	// internal/app's encodeEventsCursor/decodeEventsCursor for the format.
	NextCursor string `json:"next_cursor,omitempty"`
}

// Filters is the server-side filter /api/events.json applied to Rows, one
// field per eventstore.Filter dimension. Every field is optional and omitted
// when unset, so an unfiltered request's response carries an empty object
// rather than seven empty strings.
type Filters struct {
	Source   string `json:"source,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Action   string `json:"action,omitempty"`
	Target   string `json:"target,omitempty"`
	Severity string `json:"severity,omitempty"`
	Type     string `json:"type,omitempty"`
	Errors   bool   `json:"errors,omitempty"`
}

// Page is the server-rendered shell of /events. It carries only what must
// exist before the first poll completes — identity and the poll cadence. All
// event data arrives from Response.
type Page struct {
	ServiceName string
	Version     string
	// RefreshMs is how often the page re-polls /api/events.json, from
	// admin.status_refresh_interval.
	RefreshMs int
}
