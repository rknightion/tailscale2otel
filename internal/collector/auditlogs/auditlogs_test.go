package auditlogs_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/auditlogs"
	"github.com/rknightion/tailscale2otel/v4/internal/dedup"
	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// Compile-time assertions: *Collector is a WindowCollector, and both *fakeAPI
// and the real *tsapi.Client satisfy the collector's (unexported) api surface,
// proven by passing each into New.
var (
	_ collector.WindowCollector = (*auditlogs.Collector)(nil)
	_                           = auditlogs.New((*fakeAPI)(nil), audit.NewProcessor(), 0, 0, nil)
	_                           = auditlogs.New((*tsapi.Client)(nil), audit.NewProcessor(), 0, 0, nil)
)

// fakeAPI is a canned ConfigAuditLogs implementation standing in for
// *tsapi.Client. It records the window it was called with.
type fakeAPI struct {
	resp      audit.ConfigurationResponse
	responses []audit.ConfigurationResponse
	err       error
	calls     int
	start     time.Time
	end       time.Time
}

func (f *fakeAPI) ConfigAuditLogs(_ context.Context, start, end time.Time) (audit.ConfigurationResponse, error) {
	f.calls++
	f.start, f.end = start, end
	if f.calls <= len(f.responses) {
		return f.responses[f.calls-1], f.err
	}
	return f.resp, f.err
}

// fixed window used across the success/error tests.
var (
	from = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	to   = from.Add(time.Minute)
)

func TestCollectWindow_SuccessEmitsAndReturnsTo(t *testing.T) {
	api := &fakeAPI{resp: audit.ConfigurationResponse{
		Version: "v1",
		Tailnet: "example.com",
		Logs: []audit.Event{{
			EventTime:    from.Add(30 * time.Second),
			Type:         "CONFIG",
			EventGroupID: "g1",
			Origin:       "admin-console",
			Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com", DisplayName: "Alice"},
			Target:       audit.Target{ID: "n1", Name: "node.ts.net", Type: "NODE"},
			Action:       "CREATE",
		}},
	}}
	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil)

	hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
	if err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}
	if !hwm.Equal(to) {
		t.Fatalf("high-water mark = %v, want %v", hwm, to)
	}
	if api.calls != 1 {
		t.Fatalf("ConfigAuditLogs calls = %d, want 1", api.calls)
	}
	if !api.start.Equal(from) || !api.end.Equal(to) {
		t.Fatalf("window = [%v, %v], want [%v, %v]", api.start, api.end, from, to)
	}

	pts := rec.MetricPoints(audit.MetricAuditEvents)
	if len(pts) != 1 {
		t.Fatalf("MetricPoints(%s) = %d points, want 1", audit.MetricAuditEvents, len(pts))
	}
	if pts[0].Value != 1 {
		t.Fatalf("%s value = %v, want 1", audit.MetricAuditEvents, pts[0].Value)
	}

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("LogRecords = %d, want 1", len(logs))
	}
	if got := logs[0].Attrs["tailscale.audit.action"]; got != "CREATE" {
		t.Fatalf("audit action attr = %q, want %q", got, "CREATE")
	}
}

// TestCollectWindow_NewAuditFamiliesArePreserved exercises the response
// decoder and the real poll path with selector-shaped family values from the
// published audit vocabulary. The response type is intentionally treated as
// open: PAM and Aperture can add family markers before this collector knows
// their semantics. They must remain visible in the log, while metric labels
// retain the bounded action/origin/actor admit-sets.
func TestCollectWindow_NewAuditFamiliesArePreserved(t *testing.T) {
	const body = `{
  "version": "1.1",
  "tailnet": "synthetic-tailnet",
  "logs": [
    {
      "eventTime": "2026-08-30T12:00:00Z",
      "type": "PAM_CONNECTOR.CREATE",
      "eventGroupID": "synthetic-pam-connector",
      "origin": "BORDER0_API",
      "actor": {"id":"synthetic-actor-connector","type":"PAM_CONNECTOR"},
      "target": {"id":"synthetic-target-connector","type":"TAILNET","property":"ACL"},
      "action": "CREATE"
    },
    {
      "eventTime": "2026-08-30T12:00:01Z",
      "type": "PAM_SERVICE_ACCOUNT.CREATE.ACCESS_TOKEN",
      "eventGroupID": "synthetic-pam-service-account",
      "origin": "BORDER0_API",
      "actor": {"id":"synthetic-actor-service-account","type":"PAM_SERVICE_ACCOUNT"},
      "target": {"id":"synthetic-target-service-account","type":"TAILNET","property":"SECRET"},
      "action": "CREATE"
    },
    {
      "eventTime": "2026-08-30T12:00:02Z",
      "type": "APERTURE_POLICY_UPDATE",
      "eventGroupID": "synthetic-aperture",
      "origin": "BORDER0_API",
      "actor": {"id":"synthetic-actor-aperture","type":"USER"},
      "target": {"id":"synthetic-target-aperture","type":"TAILNET","property":"AUTH_PROVIDER"},
      "action": "UPDATE"
    },
    {
      "eventTime": "2026-08-30T12:00:03Z",
      "type": "FUTURE_AUDIT_FAMILY.NEW",
      "eventGroupID": "synthetic-future",
      "origin": "FUTURE_ORIGIN",
      "actor": {"id":"synthetic-actor-future","type":"FUTURE_ACTOR"},
      "target": {"id":"synthetic-target-future","type":"TAILNET","property":"ACL"},
      "action": "CREATE"
    }
  ]
}`

	var response audit.ConfigurationResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode synthetic audit response: %v", err)
	}
	rec := telemetrytest.New()
	c := auditlogs.New(&fakeAPI{resp: response}, audit.NewProcessor(), 0, 0, nil)
	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: %v", err)
	}

	logs := rec.LogRecords()
	if len(logs) != 4 {
		t.Fatalf("log records = %d, want 4 (one per new/future family)", len(logs))
	}
	wantLogs := map[string]struct {
		origin string
		actor  string
	}{
		"PAM_CONNECTOR.CREATE":                    {origin: "BORDER0_API", actor: "PAM_CONNECTOR"},
		"PAM_SERVICE_ACCOUNT.CREATE.ACCESS_TOKEN": {origin: "BORDER0_API", actor: "PAM_SERVICE_ACCOUNT"},
		"APERTURE_POLICY_UPDATE":                  {origin: "BORDER0_API", actor: "USER"},
		"FUTURE_AUDIT_FAMILY.NEW":                 {origin: "FUTURE_ORIGIN", actor: "FUTURE_ACTOR"},
	}
	for _, log := range logs {
		family := log.Attrs["tailscale.audit.type"]
		want, ok := wantLogs[family]
		if !ok {
			t.Errorf("unexpected family marker %q in log attrs: %#v", family, log.Attrs)
			continue
		}
		if got := log.Attrs["tailscale.audit.origin"]; got != want.origin {
			t.Errorf("%s log origin = %q, want raw %q", family, got, want.origin)
		}
		if got := log.Attrs["tailscale.actor.type"]; got != want.actor {
			t.Errorf("%s log actor type = %q, want raw %q", family, got, want.actor)
		}
	}

	seenOrigins := map[string]bool{}
	for _, point := range rec.MetricPoints(audit.MetricAuditEvents) {
		seenOrigins[point.Attrs["tailscale.audit.origin"]] = true
	}
	if want := map[string]bool{"BORDER0_API": true, "other": true}; !slices.Equal(sortedKeys(seenOrigins), sortedKeys(want)) {
		t.Errorf("normalized event origins = %v, want %v", sortedKeys(seenOrigins), sortedKeys(want))
	}

	seenActors := map[string]bool{}
	for _, point := range rec.MetricPoints(audit.MetricAuditChanges) {
		seenActors[point.Attrs["tailscale.actor.type"]] = true
	}
	if want := map[string]bool{"PAM_CONNECTOR": true, "PAM_SERVICE_ACCOUNT": true, "USER": true, "other": true}; !slices.Equal(sortedKeys(seenActors), sortedKeys(want)) {
		t.Errorf("normalized change actor types = %v, want %v", sortedKeys(seenActors), sortedKeys(want))
	}
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// TestCollectWindow_ThreadsTraceContextOntoAuditLog is the #367 acceptance
// test for the poll path: CollectWindow's ctx must reach the shared audit
// processor so a sampled span produces a native TraceID/SpanID on the
// emitted audit log, rather than being discarded in favor of
// context.Background().
func TestCollectWindow_ThreadsTraceContextOntoAuditLog(t *testing.T) {
	api := &fakeAPI{resp: audit.ConfigurationResponse{
		Logs: []audit.Event{{
			EventTime:    from.Add(30 * time.Second),
			EventGroupID: "g1",
			Actor:        audit.Actor{ID: "u1"},
			Target:       audit.Target{ID: "n1"},
			Action:       "CREATE",
		}},
	}}
	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil)

	wantTraceID := trace.TraceID{0x0f, 0x10}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     trace.SpanID{0x11},
		TraceFlags: trace.FlagsSampled,
	}))

	if _, err := c.CollectWindow(ctx, from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("LogRecords = %d, want 1", len(logs))
	}
	if logs[0].TraceID != wantTraceID.String() {
		t.Errorf("TraceID = %q, want %q", logs[0].TraceID, wantTraceID.String())
	}
}

func TestCollectWindow_ErrorPropagatesZeroTime(t *testing.T) {
	wantErr := errors.New("boom")
	api := &fakeAPI{err: wantErr}
	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil)

	hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if !hwm.IsZero() {
		t.Fatalf("high-water mark = %v, want zero", hwm)
	}
	if pts := rec.MetricPoints(audit.MetricAuditEvents); len(pts) != 0 {
		t.Fatalf("emitted %d metric points on error, want 0", len(pts))
	}
	if logs := rec.LogRecords(); len(logs) != 0 {
		t.Fatalf("emitted %d log records on error, want 0", len(logs))
	}
}

func TestCollectWindow_BoundaryEventDedupedAcrossWindows(t *testing.T) {
	// A boundary event at exactly `to` is returned by two adjacent, overlapping
	// windows. It must be emitted (counter + log) only once.
	boundary := audit.Event{
		EventTime:    to,
		Type:         "CONFIG",
		EventGroupID: "g-boundary",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com", DisplayName: "Alice"},
		Target:       audit.Target{ID: "n1", Name: "node.ts.net", Type: "NODE"},
		Action:       "CREATE",
	}
	api := &fakeAPI{resp: audit.ConfigurationResponse{
		Version: "v1",
		Tailnet: "example.com",
		Logs:    []audit.Event{boundary},
	}}
	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil)

	// First window [from, to] sees the boundary event.
	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow (window 1): unexpected error: %v", err)
	}
	// Second, adjacent window [to, to+1m] also includes the boundary event
	// because the API window is inclusive of both ends.
	if _, err := c.CollectWindow(context.Background(), to, to.Add(time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow (window 2): unexpected error: %v", err)
	}

	pts := rec.MetricPoints(audit.MetricAuditEvents)
	var total float64
	for _, p := range pts {
		total += p.Value
	}
	if total != 1 {
		t.Fatalf("%s total = %v across %d points, want 1", audit.MetricAuditEvents, total, len(pts))
	}
	if logs := rec.LogRecords(); len(logs) != 1 {
		t.Fatalf("LogRecords = %d, want 1 (boundary event emitted once)", len(logs))
	}
}

// TestCollectWindow_ConfiguredDedupCapacityEvictsOldest proves the poll
// collector uses the configured capacity for its real boundary set. With one
// slot, the second distinct event evicts the first, so the first is accepted
// again when it appears in the next window.
func TestCollectWindow_ConfiguredDedupCapacityEvictsOldest(t *testing.T) {
	a := audit.Event{
		EventTime:    from.Add(30 * time.Second),
		EventGroupID: "capacity-a",
		Type:         "CONFIG",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:       audit.Target{ID: "n1", Name: "node-a.ts.net", Type: "NODE"},
		Action:       "CREATE",
	}
	b := a
	b.EventGroupID = "capacity-b"
	b.Target.ID = "n2"
	b.Target.Name = "node-b.ts.net"
	api := &fakeAPI{responses: []audit.ConfigurationResponse{
		{Version: "v1", Tailnet: "example.com", Logs: []audit.Event{a, b}},
		{Version: "v1", Tailnet: "example.com", Logs: []audit.Event{a}},
	}}
	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil,
		auditlogs.WithDedupCapacity(1))

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() first window: %v", err)
	}
	if _, err := c.CollectWindow(context.Background(), to, to.Add(time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() second window: %v", err)
	}

	if got := len(rec.LogRecords()); got != 3 {
		t.Fatalf("log records = %d, want 3 (the evicted first event is re-admitted)", got)
	}
}

func TestCollectWindow_DistinctGroupedEventsSameTimeNotCollapsed(t *testing.T) {
	// Two distinct sub-changes sharing an eventGroupID AND an identical eventTime
	// must not be collapsed: the grouped dedup key must incorporate action/target
	// too (#97), not just groupID|time.
	t0 := from.Add(30 * time.Second)
	a := audit.Event{
		EventTime:    t0,
		EventGroupID: "grp-1",
		Type:         "CONFIG",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:       audit.Target{ID: "n1", Name: "node-a.ts.net", Type: "NODE"},
		Action:       "CREATE",
	}
	b := audit.Event{
		EventTime:    t0,
		EventGroupID: "grp-1",
		Type:         "CONFIG",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:       audit.Target{ID: "n2", Name: "node-b.ts.net", Type: "NODE"},
		Action:       "DELETE",
	}
	api := &fakeAPI{resp: audit.ConfigurationResponse{
		Version: "v1",
		Tailnet: "example.com",
		Logs:    []audit.Event{a, b},
	}}
	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil)

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}

	var total float64
	for _, p := range rec.MetricPoints(audit.MetricAuditEvents) {
		total += p.Value
	}
	if total != 2 {
		t.Fatalf("%s total = %v, want 2 (distinct grouped events not collapsed)", audit.MetricAuditEvents, total)
	}
	if logs := rec.LogRecords(); len(logs) != 2 {
		t.Fatalf("LogRecords = %d, want 2 (distinct grouped events not collapsed)", len(logs))
	}
}

func TestCollectWindow_DistinctEmptyGroupIDNotCollapsed(t *testing.T) {
	// Two distinct events that share an event time but have no eventGroupID must
	// not be collapsed into one: the dedup key must incorporate action/target.
	t0 := from.Add(30 * time.Second)
	a := audit.Event{
		EventTime: t0,
		Type:      "CONFIG",
		Origin:    "admin-console",
		Actor:     audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:    audit.Target{ID: "n1", Name: "node-a.ts.net", Type: "NODE"},
		Action:    "CREATE",
	}
	b := audit.Event{
		EventTime: t0,
		Type:      "CONFIG",
		Origin:    "admin-console",
		Actor:     audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:    audit.Target{ID: "n2", Name: "node-b.ts.net", Type: "NODE"},
		Action:    "DELETE",
	}
	api := &fakeAPI{resp: audit.ConfigurationResponse{
		Version: "v1",
		Tailnet: "example.com",
		Logs:    []audit.Event{a, b},
	}}
	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil)

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}

	pts := rec.MetricPoints(audit.MetricAuditEvents)
	var total float64
	for _, p := range pts {
		total += p.Value
	}
	if total != 2 {
		t.Fatalf("%s total = %v, want 2 (distinct events not collapsed)", audit.MetricAuditEvents, total)
	}
	if logs := rec.LogRecords(); len(logs) != 2 {
		t.Fatalf("LogRecords = %d, want 2 (distinct events not collapsed)", len(logs))
	}
}

func TestName(t *testing.T) {
	c := auditlogs.New(&fakeAPI{}, audit.NewProcessor(), 0, 0, nil)
	if got := c.Name(); got != "auditlogs" {
		t.Fatalf("Name() = %q, want %q", got, "auditlogs")
	}
}

func TestDefaultInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{"zero defaults to 60s", 0, 60 * time.Second},
		{"negative defaults to 60s", -5 * time.Second, 60 * time.Second},
		{"override honored", 30 * time.Second, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := auditlogs.New(&fakeAPI{}, audit.NewProcessor(), tt.interval, 0, nil)
			if got := c.DefaultInterval(); got != tt.want {
				t.Fatalf("DefaultInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLag(t *testing.T) {
	tests := []struct {
		name string
		lag  time.Duration
		want time.Duration
	}{
		{"zero defaults to 60s", 0, 60 * time.Second},
		{"negative defaults to 60s", -5 * time.Second, 60 * time.Second},
		{"override honored", 90 * time.Second, 90 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := auditlogs.New(&fakeAPI{}, audit.NewProcessor(), 0, tt.lag, nil)
			if got := c.Lag(); got != tt.want {
				t.Fatalf("Lag() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCollectWindow_OnIngestHookCalled verifies that after a successful window
// the onIngest hook is called exactly once with ("poll","audit",N,0) where N is
// the post-dedup event count. Two events with distinct eventKeys survive dedup.
func TestCollectWindow_OnIngestHookCalled(t *testing.T) {
	t0 := from.Add(10 * time.Second)
	t1 := from.Add(20 * time.Second)
	// Two events with distinct eventGroupIDs so both survive the overlap dedup set.
	ev1 := audit.Event{
		EventTime:    t0,
		Type:         "CONFIG",
		EventGroupID: "g-event-1",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com", DisplayName: "Alice"},
		Target:       audit.Target{ID: "n1", Name: "node-a.ts.net", Type: "NODE"},
		Action:       "CREATE",
	}
	ev2 := audit.Event{
		EventTime:    t1,
		Type:         "CONFIG",
		EventGroupID: "g-event-2",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com", DisplayName: "Alice"},
		Target:       audit.Target{ID: "n2", Name: "node-b.ts.net", Type: "NODE"},
		Action:       "DELETE",
	}
	api := &fakeAPI{resp: audit.ConfigurationResponse{
		Version: "v1",
		Tailnet: "example.com",
		Logs:    []audit.Event{ev1, ev2},
	}}

	type call struct {
		source  string
		signal  string
		records int
		bytes   int
	}
	var got []call
	hook := func(source, signal string, records, bytes int) {
		got = append(got, call{source, signal, records, bytes})
	}

	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, hook)

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("hook called %d times, want 1", len(got))
	}
	want := call{semconv.IngestSourcePoll, semconv.IngestSignalAudit, 2, 0}
	if got[0] != want {
		t.Fatalf("hook call = %+v, want %+v", got[0], want)
	}
}

// TestCollectWindow_NilOnIngestHookDoesNotPanic verifies that a nil hook does
// not cause a nil-pointer dereference on a normal window.
func TestCollectWindow_NilOnIngestHookDoesNotPanic(t *testing.T) {
	api := &fakeAPI{resp: audit.ConfigurationResponse{
		Version: "v1",
		Tailnet: "example.com",
		Logs: []audit.Event{{
			EventTime:    from.Add(30 * time.Second),
			Type:         "CONFIG",
			EventGroupID: "g1",
			Origin:       "admin-console",
			Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com", DisplayName: "Alice"},
			Target:       audit.Target{ID: "n1", Name: "node.ts.net", Type: "NODE"},
			Action:       "CREATE",
		}},
	}}
	rec := telemetrytest.New()
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil)

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}
}

// TestCollectWindow_AcceptedObserverReportsEverySourceAcceptedEvent proves the
// freshness hook follows each source-level accepted event immediately after it
// is handed to the shared processor. Removing either the per-event handoff or
// the poll/audit routing dimensions must fail this test.
func TestCollectWindow_AcceptedObserverReportsEverySourceAcceptedEvent(t *testing.T) {
	ev1 := audit.Event{
		EventTime:    from.Add(10 * time.Second),
		EventGroupID: "freshness-1",
		Type:         "CONFIG",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:       audit.Target{ID: "n1", Name: "node-a.ts.net", Type: "NODE"},
		Action:       "CREATE",
	}
	ev2 := audit.Event{
		EventTime:    from.Add(20 * time.Second),
		EventGroupID: "freshness-2",
		Type:         "CONFIG",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:       audit.Target{ID: "n2", Name: "node-b.ts.net", Type: "NODE"},
		Action:       "DELETE",
	}
	api := &fakeAPI{resp: audit.ConfigurationResponse{Logs: []audit.Event{ev1, ev2}}}
	rec := telemetrytest.New()
	var got []ingest.AcceptedEvent
	var logsAtObservation []int
	observer := func(event ingest.AcceptedEvent) {
		got = append(got, event)
		logsAtObservation = append(logsAtObservation, len(rec.LogRecords()))
	}
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil,
		auditlogs.WithAcceptedObserver(observer))

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("accepted observer calls = %d, want 2", len(got))
	}
	for i, wantTime := range []time.Time{ev1.EventTime, ev2.EventTime} {
		if got[i].Source != semconv.IngestSourcePoll {
			t.Errorf("event %d source = %q, want %q", i, got[i].Source, semconv.IngestSourcePoll)
		}
		if got[i].Signal != semconv.IngestSignalAudit {
			t.Errorf("event %d signal = %q, want %q", i, got[i].Signal, semconv.IngestSignalAudit)
		}
		if !got[i].EventTime.Equal(wantTime) {
			t.Errorf("event %d event time = %v, want %v", i, got[i].EventTime, wantTime)
		}
		if !got[i].CaptureTime.IsZero() {
			t.Errorf("event %d capture time = %v, want zero", i, got[i].CaptureTime)
		}
		if got[i].AcceptedAt.IsZero() {
			t.Errorf("event %d accepted at is zero, want non-zero", i)
		}
	}
	if want := []int{1, 2}; !slices.Equal(logsAtObservation, want) {
		t.Fatalf("log records at observer calls = %v, want %v", logsAtObservation, want)
	}
}

// TestCollectWindow_AcceptedObserverSkipsBoundaryDuplicates proves an
// inclusive-window repeat is not reported as a newly accepted source event.
func TestCollectWindow_AcceptedObserverSkipsBoundaryDuplicates(t *testing.T) {
	boundary := audit.Event{
		EventTime:    to,
		EventGroupID: "freshness-boundary",
		Type:         "CONFIG",
		Origin:       "admin-console",
		Actor:        audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:       audit.Target{ID: "n1", Name: "node.ts.net", Type: "NODE"},
		Action:       "CREATE",
	}
	api := &fakeAPI{resp: audit.ConfigurationResponse{Logs: []audit.Event{boundary}}}
	var got []ingest.AcceptedEvent
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil,
		auditlogs.WithAcceptedObserver(func(event ingest.AcceptedEvent) { got = append(got, event) }))
	rec := telemetrytest.New()

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow (first window): %v", err)
	}
	if _, err := c.CollectWindow(context.Background(), to, to.Add(time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow (second window): %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("accepted observer calls = %d, want 1", len(got))
	}
}

// TestCollectWindow_AcceptedObserverSurvivesCrossSourceSuppression proves
// source freshness records accepted poll data even when the shared processor
// suppresses its telemetry because that change arrived through another source.
func TestCollectWindow_AcceptedObserverSurvivesCrossSourceSuppression(t *testing.T) {
	ev := audit.Event{
		EventTime: from.Add(30 * time.Second),
		Type:      "CONFIG",
		Origin:    "admin-console",
		Actor:     audit.Actor{ID: "u1", LoginName: "alice@example.com"},
		Target:    audit.Target{ID: "n1", Name: "node.ts.net", Type: "NODE"},
		Action:    "CREATE",
	}
	set := dedup.New(8)
	key, ok := audit.CrossSourceKey(ev)
	if !ok || !set.Add(key) {
		t.Fatal("seed cross-source dedup key")
	}
	api := &fakeAPI{resp: audit.ConfigurationResponse{Logs: []audit.Event{ev}}}
	var got []ingest.AcceptedEvent
	c := auditlogs.New(api, audit.NewProcessor(audit.WithCrossDedup(set)), 0, 0, nil,
		auditlogs.WithAcceptedObserver(func(event ingest.AcceptedEvent) { got = append(got, event) }))
	rec := telemetrytest.New()

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}

	if len(rec.LogRecords()) != 0 {
		t.Fatalf("log records = %d, want 0 after cross-source suppression", len(rec.LogRecords()))
	}
	if len(got) != 1 {
		t.Fatalf("accepted observer calls = %d, want 1", len(got))
	}
}

// The boundary matrix (#433) proves the DECODER accepts a null or empty response
// body. This proves what happens next: the collector emits nothing at all, rather
// than a phantom zero-valued point or log record, and still advances the
// high-water mark so the window is not re-fetched forever.
//
// A zero-valued point here would be worse than no point: it is indistinguishable
// on a dashboard from "the tailnet genuinely had no audit events", while also
// creating series for label combinations the tailnet never produced.
func TestCollectWindow_EmptyAndNullResponseEmitNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp audit.ConfigurationResponse
	}{
		{"nil logs (what the real API returns for a quiet window)", audit.ConfigurationResponse{}},
		{"empty logs slice", audit.ConfigurationResponse{Logs: []audit.Event{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			c := auditlogs.New(&fakeAPI{resp: tc.resp}, audit.NewProcessor(), 0, 0, nil)

			hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
			if err != nil {
				t.Fatalf("CollectWindow: %v", err)
			}
			if !hwm.Equal(to) {
				t.Fatalf("high-water mark = %v, want %v — an empty window must still advance, or "+
					"it is re-fetched forever", hwm, to)
			}
			if pts := rec.MetricPoints(audit.MetricAuditEvents); len(pts) != 0 {
				t.Errorf("%s emitted %d point(s), want 0: %+v", audit.MetricAuditEvents, len(pts), pts)
			}
			if logs := rec.LogRecords(); len(logs) != 0 {
				t.Errorf("emitted %d log record(s), want 0: %+v", len(logs), logs)
			}
		})
	}
}

// availabilityStates returns, per operation, the single state whose gauge is 1.
func availabilityStates(t *testing.T, rec *telemetrytest.Recorder) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		op := p.Attrs["tailscale.api.operation"]
		st := p.Attrs["tailscale.api.state"]
		switch p.Value {
		case 1:
			if prev, dup := out[op]; dup {
				t.Fatalf("operation %q has two states at 1: %q and %q", op, prev, st)
			}
			out[op] = st
		case 0:
		default:
			t.Fatalf("availability gauge for %q/%q = %v, want 0 or 1", op, st, p.Value)
		}
	}
	return out
}

// TestAvailability_SuccessIsSupported pins the happy path and the injected
// clock feeding the last-probe gauge (#524).
func TestAvailability_SuccessIsSupported(t *testing.T) {
	probe := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	tr := apistate.NewTracker()
	rec := telemetrytest.New()
	api := &fakeAPI{resp: audit.ConfigurationResponse{}}
	c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil, auditlogs.WithAPIState(tr), auditlogs.WithClock(func() time.Time { return probe }))

	hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
	if err != nil {
		t.Fatalf("CollectWindow: unexpected error: %v", err)
	}
	if !hwm.Equal(to) {
		t.Fatalf("high-water mark = %v, want %v", hwm, to)
	}
	if got := availabilityStates(t, rec)["listConfigurationAuditLogs"]; got != string(apistate.StateSupported) {
		t.Errorf("availability = %q, want supported", got)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Collector != "auditlogs" || snap[0].Operation != "listConfigurationAuditLogs" {
		t.Fatalf("tracker snapshot = %+v, want one auditlogs/listConfigurationAuditLogs entry", snap)
	}
	lp := rec.MetricPoints(apistate.MetricLastProbe)
	if len(lp) != 1 || lp[0].Value != float64(probe.Unix()) {
		t.Fatalf("last_probe = %+v, want one point at %v", lp, probe.Unix())
	}
}

// TestAvailability_ErrorClassification is the #524 regression proving
// auditlogs does NOT copy flowlogs' Disposition{DisabledOn: []int{403}}.
// Configuration audit logging is not a documented feature gate on 403, so an
// ambiguous 403 must classify as scope_denied, not disabled — the whole point
// of this lane. In every case the high-water mark and error must be unchanged
// from the collector's pre-existing error-propagation behavior: a window
// collector zeroes the high-water mark on ANY fetch error so the scheduler
// retries it.
func TestAvailability_ErrorClassification(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState apistate.State
	}{
		{"401 credential rejected", &tsapi.StatusError{Code: 401}, apistate.StateCredentialRejected},
		{"403 scope denied, NOT disabled", &tsapi.StatusError{Code: 403}, apistate.StateScopeDenied},
		{"404 disabled (Classify's own default, no Disposition needed)", &tsapi.StatusError{Code: 404}, apistate.StateDisabled},
		{"400 request rejected", &tsapi.StatusError{Code: 400}, apistate.StateRequestRejected},
		{"plain transport error is transient", context.DeadlineExceeded, apistate.StateTransientFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{err: tc.err}
			rec := telemetrytest.New()
			c := auditlogs.New(api, audit.NewProcessor(), 0, 0, nil)

			hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
			if !errors.Is(err, tc.err) {
				t.Fatalf("CollectWindow() error = %v, want %v propagated unchanged", err, tc.err)
			}
			if !hwm.IsZero() {
				t.Fatalf("high-water mark = %v, want zero (checkpoint must not advance on any fetch error)", hwm)
			}
			if got := availabilityStates(t, rec)["listConfigurationAuditLogs"]; got != string(tc.wantState) {
				t.Errorf("availability = %q, want %q", got, tc.wantState)
			}
			if pts := rec.MetricPoints(audit.MetricAuditEvents); len(pts) != 0 {
				t.Errorf("%s emitted %d point(s) on error, want 0", audit.MetricAuditEvents, len(pts))
			}
			if logs := rec.LogRecords(); len(logs) != 0 {
				t.Errorf("emitted %d log record(s) on error, want 0", len(logs))
			}
		})
	}
}
