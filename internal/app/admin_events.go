package app

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/app/eventsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/app/eventshtml"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/eventstore"
)

// Bounds on what /api/events.json will do for one request, mirroring
// admin_flows.go's flow-view bounds.
const (
	defaultEventsLimit = 200
	maxEventsLimit     = 1000
	// maxEventsFilterLen bounds each filter query parameter. As with
	// admin_flows.go's maxFlowsFilterLen, an oversized filter is a 400, not a
	// silent clamp: truncating it could turn a specific search into one that
	// matches more than the operator asked for.
	maxEventsFilterLen = 128
)

// eventsCursorPrefix versions the opaque wire format of the ?cursor=
// parameter, mirroring admin_flows.go's flowsCursorPrefix.
const eventsCursorPrefix = "v1:"

// encodeEventsCursor renders seq as the opaque cursor /api/events.json hands
// back in NextCursor. A zero seq (no further page) encodes to the empty
// string, which the page's absent-cursor convention treats as "no more data".
func encodeEventsCursor(seq uint64) string {
	if seq == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(eventsCursorPrefix + strconv.FormatUint(seq, 10)))
}

// decodeEventsCursor parses the ?cursor= parameter. An absent, malformed,
// wrongly-versioned, or unparseable cursor decodes to 0 ("from the newest")
// rather than an error, mirroring decodeFlowsCursor: a cursor merely
// round-trips a value the page never inspects, and a stale one after a
// process restart (the ring's seq counter resets) must fail safe rather than
// break the view.
func decodeEventsCursor(s string) uint64 {
	if s == "" {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0
	}
	text, ok := strings.CutPrefix(string(raw), eventsCursorPrefix)
	if !ok {
		return 0
	}
	seq, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0
	}
	return seq
}

// newEventStore builds this process's shared, bounded audit/webhook event
// store (#300), or nil when the view is switched off or cannot be reached.
//
// Unlike newFlowStore (one flow store per tailnet runtime, since flow traffic
// is inherently per-tailnet), this is ONE store shared by every runtime's
// audit Processor and webhook Server — mirroring the issue's own framing,
// "bounded memory store fed by shared audit/webhook processors". The
// composition root passes the same *eventstore.Memory to every runtime via
// audit.WithStore and webhook.Options.EventStore.
//
// The store's only consumer is /events on the admin landing page, so with
// that surface disabled it would accumulate memory nobody could ever look
// at — config.Warnings() should say so, mirroring flows.enabled's advisory.
func newEventStore(cfg *config.Config) *eventstore.Memory {
	if !cfg.Events.Enabled || !cfg.Admin.Enabled || !cfg.Admin.LandingPage {
		return nil
	}
	return eventstore.NewMemory(cfg.Events.MaxEvents)
}

// eventsEnabled reports whether this process is feeding an event store, which
// is what decides whether the /events routes are registered at all. An
// unregistered route 404s, which is the honest answer: an empty result would
// read as "no events" rather than "not collecting" (mirrors flowsEnabled).
func (a *App) eventsEnabled() bool { return a.eventStore != nil }

// eventsFilterParam is one events filter query parameter and the
// eventstore.Filter/eventsdata.Filters field it feeds.
type eventsFilterParam struct {
	query string
	store *string
	wire  *string
}

// parseEventsFilter reads the events filter query parameters off q. A value
// over maxEventsFilterLen writes a 400 naming the offending parameter to w
// and returns ok=false; the caller must not write anything else to w in that
// case. Mirrors parseFlowsFilter's rationale exactly (#296).
func parseEventsFilter(w http.ResponseWriter, q url.Values) (filter eventstore.Filter, wire eventsdata.Filters, ok bool) {
	for _, p := range []eventsFilterParam{
		{"source", &filter.Source, &wire.Source},
		{"actor", &filter.Actor, &wire.Actor},
		{"action", &filter.Action, &wire.Action},
		{"target", &filter.Target, &wire.Target},
		{"severity", &filter.Severity, &wire.Severity},
		{"type", &filter.Type, &wire.Type},
	} {
		v := q.Get(p.query)
		if len(v) > maxEventsFilterLen {
			http.Error(w, fmt.Sprintf("query parameter %q exceeds %d bytes", p.query, maxEventsFilterLen), http.StatusBadRequest)
			return eventstore.Filter{}, eventsdata.Filters{}, false
		}
		*p.store = v
		*p.wire = v
	}
	if v := q.Get("errors"); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		filter.ErrorsOnly = true
		wire.Errors = true
	}
	return filter, wire, true
}

// parseEventsWindow reads the optional ?start=/?end= RFC3339 window
// parameters. Either or both may be absent: an absent start means "everything
// retained", an absent end means "now" (both handled by eventstore.Memory.Page
// itself, which treats a zero time the same way). An unparseable value is
// treated as absent rather than a 400 — the window narrows what a poll shows,
// not what it's allowed to ask for, so a malformed timestamp degrading to "no
// bound" is the fail-safe direction (mirrors clampTime's fallback-to-default
// behavior in admin_flows.go, applied here to an optional rather than a
// clamped field).
func parseEventsWindow(q url.Values) (start, end time.Time) {
	if s := q.Get("start"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			start = t.UTC()
		}
	}
	if e := q.Get("end"); e != "" {
		if t, err := time.Parse(time.RFC3339, e); err == nil {
			end = t.UTC()
		}
	}
	return start, end
}

// buildEventsResponse runs q against store and assembles the wire Response.
// Pure function (no *App/*http.Request dependency), so it is fully testable
// independent of the admin mux and of any App/tailnetRuntime wiring.
func buildEventsResponse(store *eventstore.Memory, q eventstore.Query, filters eventsdata.Filters, now time.Time) eventsdata.Response {
	page := store.Page(q)
	resp := eventsdata.Response{
		Stats:       store.Stats(),
		Rows:        page.Rows,
		Filters:     filters,
		GeneratedAt: now.Format(time.RFC3339),
		Matched:     page.Matched,
		Returned:    len(page.Rows),
		Retained:    page.Retained,
		Truncated:   page.Truncated,
		NextCursor:  encodeEventsCursor(page.NextCursor),
	}
	// The page iterates Rows unconditionally, and a nil slice marshals to null.
	if resp.Rows == nil {
		resp.Rows = []eventstore.Event{}
	}
	return resp
}

// handleEventsJSON serves the event explorer's data. Read-only, GET-only, and
// behind the same admin gate as the rest of the admin surface (mirrors
// handleFlowsJSON).
func (a *App) handleEventsJSON(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	if a.eventStore == nil {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	limit := clampInt(q.Get("limit"), defaultEventsLimit, 0, maxEventsLimit)
	filter, filters, ok := parseEventsFilter(w, q)
	if !ok {
		return // parseEventsFilter already wrote the 400.
	}
	start, end := parseEventsWindow(q)

	resp := buildEventsResponse(a.eventStore, eventstore.Query{
		Start:  start,
		End:    end,
		Limit:  limit,
		Cursor: decodeEventsCursor(q.Get("cursor")),
		Filter: filter,
	}, filters, time.Now().UTC())

	writeIndentedJSON(w, resp, a.logger, "encode events json")
}

// handleEventsPage renders the /events shell. It carries no event data — the
// page polls /api/events.json for that, so a slow or empty store never
// delays the first paint (mirrors handleFlowsPage).
func (a *App) handleEventsPage(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	if a.eventStore == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := eventsdata.Page{
		ServiceName: serviceName,
		Version:     a.version,
		RefreshMs:   int(a.cfg.Admin.StatusRefreshInterval.D().Milliseconds()),
	}
	if err := eventshtml.Render(w, page); err != nil {
		// Headers are already committed by this point; there is nothing to
		// send but a log line.
		a.logger.Error("render events page", "error", err)
	}
}

// eventStoreInfo summarizes the event store for the status page (#300). It is
// the only thing that tells the page whether to offer the /events link at all:
// the explorer's routes are registered only when a store is being fed, so
// linking unconditionally would give the operator a link that 404s.
//
// Evicted is surfaced deliberately. The store is a bounded ring, so a busy
// tailnet silently drops the oldest events once it is full — and a timeline
// missing its start reads as "nothing happened before this" rather than "the
// window ran out", which is the wrong conclusion to hand someone mid-incident.
func (a *App) eventStoreInfo() statusdata.EventStoreInfo {
	if a.eventStore == nil {
		return statusdata.EventStoreInfo{}
	}
	st := a.eventStore.Stats()
	return statusdata.EventStoreInfo{
		Enabled:  true,
		Capacity: st.Capacity,
		Retained: st.Retained,
		Recorded: st.Recorded,
		Evicted:  st.Evicted,
	}
}
