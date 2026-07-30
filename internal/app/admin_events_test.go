package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/app/eventsdata"
	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/eventstore"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// eventsTestApp builds an App with the event explorer enabled on a loopback
// admin bind (mirrors flowsTestApp in admin_flows_test.go), and directly
// assigns a.eventStore since this test lives in package app.
func eventsTestApp(t *testing.T, tune func(*config.Config)) *App {
	t.Helper()
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Events.Enabled = true
	if tune != nil {
		tune(cfg)
	}
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	a.eventStore = newEventStore(cfg)
	return a
}

// getEvents issues a GET against the real admin mux and decodes the response.
func getEvents(t *testing.T, a *App, query string) (*httptest.ResponseRecorder, eventsdata.Response) {
	t.Helper()
	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodGet, "/api/events.json"+query))
	var got eventsdata.Response
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode /api/events.json: %v\nbody: %s", err, w.Body.String())
		}
	}
	return w, got
}

func TestNewEventStore_GatedOnAdminAndLandingPage(t *testing.T) {
	tests := []struct {
		name        string
		eventsOn    bool
		adminOn     bool
		landingPage bool
		wantNil     bool
	}{
		{name: "reachable", eventsOn: true, adminOn: true, landingPage: true},
		{name: "events disabled", eventsOn: false, adminOn: true, landingPage: true, wantNil: true},
		{name: "admin disabled", eventsOn: true, adminOn: false, landingPage: true, wantNil: true},
		{name: "landing page disabled", eventsOn: true, adminOn: true, landingPage: false, wantNil: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Events.Enabled = tc.eventsOn
			cfg.Admin.Enabled = tc.adminOn
			cfg.Admin.LandingPage = tc.landingPage
			store := newEventStore(cfg)
			if (store == nil) != tc.wantNil {
				t.Errorf("newEventStore() nil = %v, want %v", store == nil, tc.wantNil)
			}
		})
	}
}

func TestEventsJSON_ServesWhatTheProcessorRecorded(t *testing.T) {
	a := eventsTestApp(t, nil)
	a.eventStore.Record(eventstore.Event{
		Time:      time.Now().UTC(),
		Source:    eventstore.SourceAudit,
		Action:    "CREATE",
		Type:      "NODE",
		ActorID:   "u1",
		ActorName: "alice@example.com",
		TargetID:  "n1",
		Severity:  eventstore.SeverityInfo,
	})

	w, got := getEvents(t, a, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/events.json = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %+v, want 1", got.Rows)
	}
	if got.Rows[0].Action != "CREATE" || got.Rows[0].ActorName != "alice@example.com" {
		t.Errorf("row = %+v, unexpected", got.Rows[0])
	}
	if got.Stats.Retained != 1 {
		t.Errorf("stats.retained = %d, want 1", got.Stats.Retained)
	}
	if got.Matched != 1 || got.Returned != 1 {
		t.Errorf("matched/returned = %d/%d, want 1/1", got.Matched, got.Returned)
	}
}

// TestEventsJSON_RowsNeverNull guards the page's unconditional iteration: an
// empty store must still answer with "rows":[] rather than "rows":null.
func TestEventsJSON_RowsNeverNull(t *testing.T) {
	a := eventsTestApp(t, nil)
	w, _ := getEvents(t, a, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/events.json = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !json.Valid(w.Body.Bytes()) {
		t.Fatalf("invalid JSON: %s", w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw["rows"] == nil {
		t.Error(`"rows" is null, want []`)
	}
}

func TestEventsJSON_FilterBySource(t *testing.T) {
	a := eventsTestApp(t, nil)
	a.eventStore.Record(eventstore.Event{Source: eventstore.SourceAudit, Action: "CREATE"})
	a.eventStore.Record(eventstore.Event{Source: eventstore.SourceWebhook, Action: "nodeCreated"})

	_, got := getEvents(t, a, "?source=webhook")
	if got.Matched != 1 || len(got.Rows) != 1 {
		t.Fatalf("matched/rows = %d/%+v, want 1 webhook row", got.Matched, got.Rows)
	}
	if got.Rows[0].Source != eventstore.SourceWebhook {
		t.Errorf("row source = %q, want webhook", got.Rows[0].Source)
	}
	if got.Filters.Source != "webhook" {
		t.Errorf("echoed filter source = %q, want webhook", got.Filters.Source)
	}
}

// TestEventsJSON_OversizedFilterIs400 mirrors admin_flows.go's
// maxFlowsFilterLen behavior (#296): an oversized filter value is refused,
// not silently clamped, since clamping could turn a specific search into one
// that matches more than the operator asked for.
func TestEventsJSON_OversizedFilterIs400(t *testing.T) {
	a := eventsTestApp(t, nil)
	q := url.Values{}
	q.Set("actor", strings.Repeat("a", maxEventsFilterLen+1))
	w, _ := getEvents(t, a, "?"+q.Encode())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an oversized filter", w.Code)
	}
}

func TestEventsJSON_ErrorsOnlyFilter(t *testing.T) {
	a := eventsTestApp(t, nil)
	a.eventStore.Record(eventstore.Event{Action: "CREATE"})
	a.eventStore.Record(eventstore.Event{Action: "CREATE", Error: "boom"})

	_, got := getEvents(t, a, "?errors=1")
	if got.Matched != 1 {
		t.Fatalf("matched = %d, want 1", got.Matched)
	}
	if got.Rows[0].Error != "boom" {
		t.Errorf("row error = %q, want boom", got.Rows[0].Error)
	}
}

func TestEventsJSON_CursorPagination(t *testing.T) {
	a := eventsTestApp(t, nil)
	base := time.Now().Add(-time.Hour).UTC()
	for i := range 5 {
		a.eventStore.Record(eventstore.Event{Time: base.Add(time.Duration(i) * time.Minute), Action: "CREATE"})
	}
	_, first := getEvents(t, a, "?limit=2")
	if len(first.Rows) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, want 2 rows and a cursor", first)
	}
	_, second := getEvents(t, a, "?limit=2&cursor="+first.NextCursor)
	if len(second.Rows) != 2 {
		t.Fatalf("second page rows = %d, want 2", len(second.Rows))
	}
	for _, a := range first.Rows {
		for _, b := range second.Rows {
			if a.Time.Equal(b.Time) {
				t.Errorf("page overlap: %v appears on both pages", a.Time)
			}
		}
	}
}

func TestEventsJSON_TruncatedWhenRingAtCapacity(t *testing.T) {
	a := eventsTestApp(t, func(c *config.Config) { c.Events.MaxEvents = 100 })
	for range 100 {
		a.eventStore.Record(eventstore.Event{Action: "CREATE"})
	}
	_, got := getEvents(t, a, "")
	if !got.Truncated {
		t.Error("truncated = false, want true: the ring is at its configured capacity")
	}
	if got.Stats.Evicted != 0 {
		t.Errorf("evicted = %d, want 0 (exactly at capacity, nothing evicted yet)", got.Stats.Evicted)
	}
	a.eventStore.Record(eventstore.Event{Action: "ONE_MORE"})
	_, got = getEvents(t, a, "")
	if got.Stats.Evicted != 1 {
		t.Errorf("evicted = %d, want 1 after exceeding capacity", got.Stats.Evicted)
	}
}

func TestEventsRoutes_404WhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	cfg.Events.Enabled = false
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	// a.eventStore intentionally left nil, as newEventStore(cfg) would return.

	for _, path := range []string{"/events", "/api/events.json"} {
		w := httptest.NewRecorder()
		a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodGet, path))
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s with events disabled = %d, want 404 (an unregistered route, not an empty result)", path, w.Code)
		}
	}
}

func TestEventsPage_Renders(t *testing.T) {
	a := eventsTestApp(t, nil)
	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodGet, "/events"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /events = %d, want 200: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q, want text/html; charset=utf-8", ct)
	}
}

// TestEventsRoutes_PostNotAllowed exercises the built mux, not a bare handler
// call — driving the mux is what actually proves the route is registered
// GET-only and admin-gated together, rather than testing getOnly() in
// isolation.
func TestEventsRoutes_PostNotAllowed(t *testing.T) {
	a := eventsTestApp(t, nil)
	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodPost, "/api/events.json"))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/events.json = %d, want 405", w.Code)
	}
}

func TestEventsCursor_RoundTrip(t *testing.T) {
	for _, seq := range []uint64{0, 1, 42, 1 << 40} {
		enc := encodeEventsCursor(seq)
		if seq == 0 {
			if enc != "" {
				t.Errorf("encodeEventsCursor(0) = %q, want empty", enc)
			}
			continue
		}
		if got := decodeEventsCursor(enc); got != seq {
			t.Errorf("round trip %d -> %q -> %d", seq, enc, got)
		}
	}
}

func TestEventsCursor_MalformedDecodesToZero(t *testing.T) {
	for _, bad := range []string{"", "not-base64!!", "dGVzdA", "djI6NDI"} { // "v2:42" base64url'd
		if got := decodeEventsCursor(bad); got != 0 {
			t.Errorf("decodeEventsCursor(%q) = %d, want 0 (fail safe)", bad, got)
		}
	}
}

// TestEventStore_IsWiredIntoTheAuditProcessor drives the REAL composition root
// (newApp -> buildProcessDeps -> addRuntime) and asserts an event processed by
// a runtime's own audit processor is visible through /api/events.json.
//
// Every other test in this file assigns a.eventStore directly, which is what
// makes them useless as a wiring guard: assigning a FRESH store replaces the
// one buildProcessDeps handed to audit.NewProcessor, so the processor keeps
// feeding the original and the handler reads the replacement. Verified by
// mutation — deleting BOTH audit.WithStore(d.eventStore) in tailnetruntime.go
// and wh.EventStore = a.eventStore in collectors.go left the entire suite
// green, so the explorer could have shipped permanently empty. This test is
// the only thing that fails.
func TestEventStore_IsWiredIntoTheAuditProcessor(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Events.Enabled = true
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)
	// Deliberately NOT overwriting a.eventStore: the store under test is the one
	// the composition root built and handed to the audit processor.
	if a.eventStore == nil {
		t.Fatal("composition root built no event store with events.enabled=true")
	}

	a.primary().auditProc.Process(audit.Event{
		EventTime: time.Now(),
		Type:      "test.wiring",
		Action:    "UPDATE",
		Origin:    "ADMIN_CONSOLE",
		Actor:     audit.Actor{ID: "uid-wiring", LoginName: "wiring@example.com", Type: "USER"},
		Target:    audit.Target{ID: "tgt-wiring", Name: "wiring-target", Type: "DEVICE"},
	}, rec.Emitter())

	_, got := getEvents(t, a, "")
	for _, row := range got.Rows {
		if row.ActorName == "wiring@example.com" || row.TargetName == "wiring-target" {
			return
		}
	}
	t.Fatalf("an event processed by the runtime's audit processor never reached /api/events.json; got %d rows — "+
		"the store is not wired into audit.NewProcessor", len(got.Rows))
}

// TestEventStore_IsWiredIntoTheWebhookReceiver is the webhook half of the
// wiring guard above. The explorer's whole point is one timeline across BOTH
// sources, so a store fed by audit alone still fails the issue's premise while
// looking populated — which is why this needs its own assertion rather than
// riding on the audit one.
//
// Verified by mutation: deleting wh.EventStore = a.eventStore in collectors.go
// leaves every other test green, this one included until it existed.
func TestEventStore_IsWiredIntoTheWebhookReceiver(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Events.Enabled = true
	cfg.Webhook.Enabled = true
	cfg.Webhook.Listen = "127.0.0.1:0"
	cfg.Webhook.Path = "/webhook"
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)
	if a.webhookSrv == nil {
		t.Fatal("webhook server not built")
	}
	if a.eventStore == nil {
		t.Fatal("composition root built no event store with events.enabled=true")
	}

	body := `[{"timestamp":"2024-06-06T15:25:26Z","version":1,"type":"nodeCreated",` +
		`"tailnet":"example.com","message":"wiring-probe","data":{"nodeID":"n-wiring"}}]`
	w := httptest.NewRecorder()
	a.webhookSrv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook POST status = %d, want 200", w.Code)
	}

	_, got := getEvents(t, a, "")
	for _, row := range got.Rows {
		if row.Source == eventstore.SourceWebhook {
			return
		}
	}
	t.Fatalf("a webhook delivery accepted by the receiver never reached /api/events.json; got %d rows — "+
		"the store is not wired into webhook.Options", len(got.Rows))
}
