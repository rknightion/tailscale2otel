package auditlogs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/collector/auditlogs"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
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
	resp  audit.ConfigurationResponse
	err   error
	calls int
	start time.Time
	end   time.Time
}

func (f *fakeAPI) ConfigAuditLogs(_ context.Context, start, end time.Time) (audit.ConfigurationResponse, error) {
	f.calls++
	f.start, f.end = start, end
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
