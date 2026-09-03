package audit_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v5/internal/audit"
	"github.com/rknightion/tailscale2otel/v5/internal/dedup"
	"github.com/rknightion/tailscale2otel/v5/internal/eventstore"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func sampleEvent() audit.Event {
	return audit.Event{
		EventTime:    time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
		Type:         "CONFIG",
		EventGroupID: "abc123",
		Origin:       "ADMIN_CONSOLE",
		Actor: audit.Actor{
			ID:          "u1",
			Type:        "USER",
			LoginName:   "a@example.com",
			DisplayName: "Lion",
		},
		Target: audit.Target{
			ID:       "n1",
			Name:     "node.ts.net",
			Type:     "NODE",
			Property: "ALLOWED_IPS",
		},
		Action:        "CREATE",
		ActionDetails: "x",
	}
}

func TestProcessEmitsLogAndCounter(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor()

	p.Process(sampleEvent(), rec.Emitter())

	// --- Log assertions ---
	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1", len(logs))
	}
	lr := logs[0]
	if lr.EventName != "tailscale.config.audit" {
		t.Fatalf("event.name = %q, want tailscale.config.audit", lr.EventName)
	}
	// Body must contain the action and target type (non-PII enums) but NOT
	// the actor login or target name — those are PII identifiers that live in
	// attributes (user.name, tailscale.target.name) where they are
	// subject to pii_filter redaction.
	wantBody := "CREATE on NODE.ALLOWED_IPS via ADMIN_CONSOLE"
	if lr.Body != wantBody {
		t.Fatalf("body = %q, want %q", lr.Body, wantBody)
	}
	if strings.Contains(lr.Body, "a@example.com") {
		t.Fatalf("body %q must not contain actor login (PII)", lr.Body)
	}
	if strings.Contains(lr.Body, "node.ts.net") {
		t.Fatalf("body %q must not contain target name (PII)", lr.Body)
	}
	if lr.SeverityText != "INFO" {
		t.Fatalf("severity = %q, want INFO", lr.SeverityText)
	}
	wantAttrs := map[string]string{
		"tailscale.audit.action":         "CREATE",
		"tailscale.audit.origin":         "ADMIN_CONSOLE",
		"tailscale.audit.event_group_id": "abc123",
		"user.id":                        "u1",
		"user.name":                      "a@example.com",
		"user.full_name":                 "Lion",
		"tailscale.actor.type":           "USER",
		"tailscale.target.id":            "n1",
		"tailscale.target.name":          "node.ts.net",
		"tailscale.target.type":          "NODE",
		"tailscale.target.property":      "ALLOWED_IPS",
		"tailscale.audit.details":        "x",
	}
	for k, want := range wantAttrs {
		if got := lr.Attrs[k]; got != want {
			t.Errorf("log attr %q = %q, want %q", k, got, want)
		}
	}
	// No error => no "error.message" attr, and no old/new since they were empty.
	if _, ok := lr.Attrs["error.message"]; ok {
		t.Errorf("unexpected error attr present: %q", lr.Attrs["error.message"])
	}
	if _, ok := lr.Attrs["tailscale.audit.old"]; ok {
		t.Errorf("unexpected old attr present: %q", lr.Attrs["tailscale.audit.old"])
	}
	if _, ok := lr.Attrs["tailscale.audit.new"]; ok {
		t.Errorf("unexpected new attr present: %q", lr.Attrs["tailscale.audit.new"])
	}

	// --- Counter assertions ---
	pts := rec.MetricPoints(audit.MetricAuditEvents)
	if len(pts) != 1 {
		t.Fatalf("metric points = %d, want 1", len(pts))
	}
	mp := pts[0]
	if mp.Name != audit.MetricAuditEvents {
		t.Fatalf("metric name = %q, want %q", mp.Name, audit.MetricAuditEvents)
	}
	if mp.Unit != "{event}" {
		t.Fatalf("metric unit = %q, want {event}", mp.Unit)
	}
	if mp.Kind != "sum" || !mp.Monotonic {
		t.Fatalf("metric kind=%q monotonic=%v, want sum/true", mp.Kind, mp.Monotonic)
	}
	if mp.Value != 1 {
		t.Fatalf("metric value = %v, want 1", mp.Value)
	}
	if mp.Attrs["tailscale.audit.action"] != "CREATE" {
		t.Errorf("metric action attr = %q, want CREATE", mp.Attrs["tailscale.audit.action"])
	}
	if mp.Attrs["tailscale.audit.origin"] != "ADMIN_CONSOLE" {
		t.Errorf("metric origin attr = %q, want ADMIN_CONSOLE", mp.Attrs["tailscale.audit.origin"])
	}
	// Low-cardinality only: actor/target must NOT be on the metric.
	for _, k := range []string{"user.id", "user.name", "tailscale.target.id", "tailscale.target.name"} {
		if _, ok := mp.Attrs[k]; ok {
			t.Errorf("metric should not carry high-cardinality attr %q (=%q)", k, mp.Attrs[k])
		}
	}
}

func TestProcessPreservesDeferredAuditSemantics(t *testing.T) {
	acceptedAt := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	eventTime := acceptedAt.Add(-12 * time.Minute)
	deferredAt := acceptedAt.Add(-10 * time.Minute)
	isEphemeral := false

	ev := sampleEvent()
	ev.Type = "CONFIG"
	ev.EventTime = eventTime
	ev.DeferredAt = deferredAt
	ev.Actor.Tags = []string{" zeta ", "alpha", "", "alpha", " beta "}
	ev.Target.IsEphemeral = &isEphemeral

	rec := telemetrytest.New()
	audit.NewProcessor(audit.WithClock(func() time.Time { return acceptedAt })).Process(ev, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1", len(logs))
	}
	attrs := logs[0].Attrs
	for key, want := range map[string]string{
		"tailscale.audit.type":        "CONFIG",
		"tailscale.actor.tags":        "alpha,beta,zeta",
		"tailscale.target.ephemeral":  "false",
		"tailscale.audit.deferred_at": deferredAt.Format(time.RFC3339Nano),
	} {
		if got := attrs[key]; got != want {
			t.Errorf("log attr %q = %q, want %q", key, got, want)
		}
	}
	if got, want := ev.Actor.Tags, []string{" zeta ", "alpha", "", "alpha", " beta "}; !slices.Equal(got, want) {
		t.Errorf("Process mutated actor tags: got %q, want %q", got, want)
	}

	assertSingleDelayHistogram(t, rec, audit.MetricAuditDeferredDelay, 2*time.Minute)
	assertSingleDelayHistogram(t, rec, audit.MetricAuditProcessingDelay, 10*time.Minute)
}

func TestProcessOmitsAbsentAuditSemantics(t *testing.T) {
	ev := sampleEvent()
	ev.Type = ""
	rec := telemetrytest.New()
	audit.NewProcessor(audit.WithClock(func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) })).Process(ev, rec.Emitter())

	attrs := rec.LogRecords()[0].Attrs
	for _, key := range []string{
		"tailscale.audit.type",
		"tailscale.actor.tags",
		"tailscale.target.ephemeral",
		"tailscale.audit.deferred_at",
	} {
		if _, ok := attrs[key]; ok {
			t.Errorf("unexpected absent-semantics attribute %q: %q", key, attrs[key])
		}
	}
	if got := rec.MetricPoints(audit.MetricAuditDeferredDelay); len(got) != 0 {
		t.Fatalf("deferred delay points = %d, want 0", len(got))
	}
	if got := rec.MetricPoints(audit.MetricAuditProcessingDelay); len(got) != 1 {
		t.Fatalf("processing delay points = %d, want 1", len(got))
	}
}

func TestProcessDecodedAuditRecordsHaveEquivalentSignals(t *testing.T) {
	const body = `{
		"eventTime":"2026-07-26T11:48:00Z",
		"deferredAt":"2026-07-26T11:50:00Z",
		"type":"CONFIG",
		"eventGroupID":"group",
		"origin":"ADMIN_CONSOLE",
		"actor":{"id":"n1","type":"NODE","tags":["prod","edge"]},
		"target":{"id":"n2","type":"NODE","isEphemeral":false,"property":"NETWORK_FLOW_LOGGING"},
		"action":"UPDATE"
	}`
	var poll, hec audit.Event
	if err := json.Unmarshal([]byte(body), &poll); err != nil {
		t.Fatalf("decode poll event: %v", err)
	}
	if err := json.Unmarshal([]byte(body), &hec); err != nil {
		t.Fatalf("decode HEC event: %v", err)
	}
	acceptedAt := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	pollRec := telemetrytest.New()
	hecRec := telemetrytest.New()
	audit.NewProcessor(audit.WithClock(func() time.Time { return acceptedAt })).Process(poll, pollRec.Emitter())
	audit.NewProcessor(audit.WithClock(func() time.Time { return acceptedAt })).Process(hec, hecRec.Emitter())

	pollLogs := pollRec.LogRecords()
	hecLogs := hecRec.LogRecords()
	for i := range pollLogs {
		// ObservedTimestamp is assigned by the OTEL SDK at export time, not by
		// the shared audit processor's input record.
		pollLogs[i].ObservedTimestamp = time.Time{}
	}
	for i := range hecLogs {
		hecLogs[i].ObservedTimestamp = time.Time{}
	}
	if got, want := pollLogs, hecLogs; !reflect.DeepEqual(got, want) {
		t.Errorf("log records differ for equivalently decoded records:\n poll: %#v\n  HEC: %#v", got, want)
	}
	for _, name := range []string{
		audit.MetricAuditEvents,
		audit.MetricAuditChanges,
		audit.MetricAuditDeferredDelay,
		audit.MetricAuditProcessingDelay,
	} {
		if got, want := pollRec.MetricPoints(name), hecRec.MetricPoints(name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s differs for equivalently decoded records:\n poll: %#v\n  HEC: %#v", name, got, want)
		}
	}
}

func TestProcessDelayHistogramsRejectInvalidDurations(t *testing.T) {
	acceptedAt := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ev := sampleEvent()
	ev.EventTime = acceptedAt.Add(-time.Minute)
	ev.DeferredAt = ev.EventTime.Add(-time.Second)

	rec := telemetrytest.New()
	audit.NewProcessor(audit.WithClock(func() time.Time { return acceptedAt })).Process(ev, rec.Emitter())
	if got := rec.MetricPoints(audit.MetricAuditDeferredDelay); len(got) != 0 {
		t.Fatalf("deferred delay points = %d, want 0 for negative source duration", len(got))
	}
	assertSingleDelayHistogram(t, rec, audit.MetricAuditProcessingDelay, time.Minute)

	ev.EventTime = acceptedAt.Add(time.Second)
	ev.DeferredAt = time.Time{}
	rec = telemetrytest.New()
	audit.NewProcessor(audit.WithClock(func() time.Time { return acceptedAt })).Process(ev, rec.Emitter())
	if got := rec.MetricPoints(audit.MetricAuditProcessingDelay); len(got) != 0 {
		t.Fatalf("processing delay points = %d, want 0 for future source time", len(got))
	}
}

func assertSingleDelayHistogram(t *testing.T, rec *telemetrytest.Recorder, name string, want time.Duration) {
	t.Helper()
	pts := rec.MetricPoints(name)
	if len(pts) != 1 {
		t.Fatalf("%s points = %d, want 1", name, len(pts))
	}
	pt := pts[0]
	if pt.Kind != "histogram" {
		t.Fatalf("%s kind = %q, want histogram", name, pt.Kind)
	}
	if pt.Unit != "s" {
		t.Errorf("%s unit = %q, want s", name, pt.Unit)
	}
	if pt.Count != 1 {
		t.Errorf("%s count = %d, want 1", name, pt.Count)
	}
	if pt.Value != want.Seconds() {
		t.Errorf("%s sum = %v, want %v", name, pt.Value, want.Seconds())
	}
	if len(pt.Attrs) != 0 {
		t.Errorf("%s attrs = %v, want none", name, pt.Attrs)
	}
}

func TestProcessErrorRaisesSeverityAndErrorAttr(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor()

	ev := sampleEvent()
	ev.Error = "permission denied"
	ev.Old = json.RawMessage(`"1.2.3.4/32"`)
	ev.New = json.RawMessage(`"5.6.7.8/32"`)
	p.Process(ev, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1", len(logs))
	}
	lr := logs[0]
	if lr.SeverityText != "WARN" {
		t.Fatalf("severity = %q, want WARN", lr.SeverityText)
	}
	if lr.Attrs["error.message"] != "permission denied" {
		t.Fatalf("error.message attr = %q, want permission denied", lr.Attrs["error.message"])
	}
	if lr.Attrs["tailscale.audit.old"] != "1.2.3.4/32" {
		t.Fatalf("old attr = %q, want 1.2.3.4/32", lr.Attrs["tailscale.audit.old"])
	}
	if lr.Attrs["tailscale.audit.new"] != "5.6.7.8/32" {
		t.Fatalf("new attr = %q, want 5.6.7.8/32", lr.Attrs["tailscale.audit.new"])
	}
}

func TestProcessAllEmitsPerEvent(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor()

	a := sampleEvent()
	b := sampleEvent()
	b.Action = "DELETE"
	b.EventGroupID = "def456"
	resp := audit.ConfigurationResponse{
		Version: "1.1",
		Tailnet: "example.com",
		Logs:    []audit.Event{a, b},
	}

	p.ProcessAll(resp, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 2 {
		t.Fatalf("log records = %d, want 2", len(logs))
	}

	pts := rec.MetricPoints(audit.MetricAuditEvents)
	var total float64
	for _, mp := range pts {
		total += mp.Value
	}
	if total != 2 {
		t.Fatalf("counter total = %v, want 2", total)
	}
}

// polymorphicConfigBody mirrors real Tailscale audit data where old/new are
// polymorphic: a JSON string, object, array, or null/absent. Decoding must not
// fail, and each variant must render to the expected attribute string.
const polymorphicConfigBody = `{
  "version": "1.1",
  "tailnetId": "example.com",
  "logs": [
    {
      "eventTime": "2026-06-02T19:00:05.558078907Z",
      "type": "CONFIG",
      "eventGroupID": "g-string",
      "origin": "NODE",
      "actor": {"id":"u1","type":"USER","loginName":"alice@example.com","displayName":"Alice"},
      "target": {"id":"n1","name":"node.ts.net","type":"NODE","property":"MACHINE_NAME"},
      "action": "UPDATE",
      "old": "",
      "new": "service-node"
    },
    {
      "eventTime": "2026-06-02T19:00:05.558376389Z",
      "type": "CONFIG",
      "eventGroupID": "g-object",
      "origin": "ADMIN_CONSOLE",
      "actor": {"id":"u1","type":"USER","loginName":"alice@example.com","displayName":"Alice"},
      "target": {"id":"n1","name":"node.ts.net","type":"NODE","property":"POSTURE"},
      "action": "UPDATE",
      "old": {"PostureDisabled":false},
      "new": {"PostureDisabled":true}
    },
    {
      "eventTime": "2026-06-02T19:00:05.558444283Z",
      "type": "CONFIG",
      "eventGroupID": "g-array",
      "origin": "NODE",
      "actor": {"id":"u1","type":"USER","loginName":"alice@example.com","displayName":"Alice"},
      "target": {"id":"n1","name":"node.ts.net","type":"NODE","property":"ACL_TAGS"},
      "action": "UPDATE",
      "new": ["tag:grafana-pdc"]
    },
    {
      "eventTime": "2026-06-02T19:00:05.558528518Z",
      "type": "CONFIG",
      "eventGroupID": "g-null",
      "origin": "NODE",
      "actor": {"id":"u1","type":"USER","loginName":"alice@example.com","displayName":"Alice"},
      "target": {"id":"n1","name":"node.ts.net","type":"NODE"},
      "action": "CREATE",
      "old": null,
      "new": null
    }
  ]
}`

func TestProcessAllRendersPolymorphicOldNew(t *testing.T) {
	var resp audit.ConfigurationResponse
	if err := json.Unmarshal([]byte(polymorphicConfigBody), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Logs) != 4 {
		t.Fatalf("logs = %d, want 4", len(resp.Logs))
	}

	rec := telemetrytest.New()
	p := audit.NewProcessor()
	p.ProcessAll(resp, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 4 {
		t.Fatalf("log records = %d, want 4", len(logs))
	}

	// Index log records by event_group_id for stable lookup.
	byGroup := map[string]telemetrytest.LogRecord{}
	for _, lr := range logs {
		byGroup[lr.Attrs["tailscale.audit.event_group_id"]] = lr
	}

	// (a) JSON string new -> unquoted string; empty old absent.
	str := byGroup["g-string"]
	if got := str.Attrs["tailscale.audit.new"]; got != "service-node" {
		t.Errorf("string new = %q, want unquoted service-node", got)
	}
	if _, ok := str.Attrs["tailscale.audit.old"]; ok {
		t.Errorf("empty-string old should be absent, got %q", str.Attrs["tailscale.audit.old"])
	}

	// (b) object new/old -> compact raw JSON string.
	obj := byGroup["g-object"]
	if got := obj.Attrs["tailscale.audit.new"]; got != `{"PostureDisabled":true}` {
		t.Errorf("object new = %q, want {\"PostureDisabled\":true}", got)
	}
	if got := obj.Attrs["tailscale.audit.old"]; got != `{"PostureDisabled":false}` {
		t.Errorf("object old = %q, want {\"PostureDisabled\":false}", got)
	}

	// (c) array new -> compact raw JSON string.
	arr := byGroup["g-array"]
	if got := arr.Attrs["tailscale.audit.new"]; got != `["tag:grafana-pdc"]` {
		t.Errorf("array new = %q, want [\"tag:grafana-pdc\"]", got)
	}
	if _, ok := arr.Attrs["tailscale.audit.old"]; ok {
		t.Errorf("absent old should be absent, got %q", arr.Attrs["tailscale.audit.old"])
	}

	// (d) null/absent old & new -> both attributes absent.
	nul := byGroup["g-null"]
	if _, ok := nul.Attrs["tailscale.audit.new"]; ok {
		t.Errorf("null new should be absent, got %q", nul.Attrs["tailscale.audit.new"])
	}
	if _, ok := nul.Attrs["tailscale.audit.old"]; ok {
		t.Errorf("null old should be absent, got %q", nul.Attrs["tailscale.audit.old"])
	}

	// Counter still increments once per event.
	pts := rec.MetricPoints(audit.MetricAuditEvents)
	var total float64
	for _, mp := range pts {
		total += mp.Value
	}
	if total != 4 {
		t.Fatalf("counter total = %v, want 4", total)
	}
}

func TestWithStore_NilIsNoOp(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor(audit.WithStore(nil))
	p.Process(sampleEvent(), rec.Emitter())
	// No panic, no observable difference: this is exercised implicitly by every
	// other test in this file constructing NewProcessor() with no options
	// (nil store by default). This test names the case explicitly.
}

func TestWithStore_FeedsTheEventStoreAfterEmitting(t *testing.T) {
	rec := telemetrytest.New()
	store := eventstore.NewMemory(10)
	p := audit.NewProcessor(audit.WithStore(store))

	ev := sampleEvent()
	ev.Error = "permission denied"
	ev.Old = json.RawMessage(`"1.2.3.4/32"`)
	ev.New = json.RawMessage(`"5.6.7.8/32"`)
	p.Process(ev, rec.Emitter())

	// The OTLP path must be entirely unaffected by attaching a store.
	if len(rec.LogRecords()) != 1 {
		t.Fatalf("log records = %d, want 1 (store attach must not change emission)", len(rec.LogRecords()))
	}

	page := store.Page(eventstore.Query{Limit: 10})
	if len(page.Rows) != 1 {
		t.Fatalf("store rows = %d, want 1", len(page.Rows))
	}
	got := page.Rows[0]
	if got.Source != eventstore.SourceAudit {
		t.Errorf("source = %q, want audit", got.Source)
	}
	if got.Action != "CREATE" || got.Type != "CONFIG" || got.Origin != "ADMIN_CONSOLE" {
		t.Errorf("action/type/origin = %q/%q/%q, want CREATE/CONFIG/ADMIN_CONSOLE", got.Action, got.Type, got.Origin)
	}
	if got.ActorID != "u1" || got.ActorName != "a@example.com" || got.ActorType != "USER" {
		t.Errorf("actor = %q/%q/%q, want u1/a@example.com/USER", got.ActorID, got.ActorName, got.ActorType)
	}
	if got.TargetID != "n1" || got.TargetName != "node.ts.net" || got.TargetType != "NODE" || got.TargetProperty != "ALLOWED_IPS" {
		t.Errorf("target = %+v, unexpected", got)
	}
	if got.Severity != eventstore.SeverityWarn {
		t.Errorf("severity = %q, want warn (event carries an Error)", got.Severity)
	}
	if got.Error != "permission denied" {
		t.Errorf("error = %q, want permission denied", got.Error)
	}
	wantSummary := "CREATE on NODE.ALLOWED_IPS via ADMIN_CONSOLE"
	if got.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", got.Summary, wantSummary)
	}
	wantDetails := "old: 1.2.3.4/32\nnew: 5.6.7.8/32"
	if got.Details != wantDetails {
		t.Errorf("details = %q, want %q", got.Details, wantDetails)
	}
}

func TestWithStore_DedupedEventNeverReachesTheStore(t *testing.T) {
	rec := telemetrytest.New()
	store := eventstore.NewMemory(10)
	dedupSet := dedup.New(100)
	p := audit.NewProcessor(audit.WithDedup(dedupSet), audit.WithStore(store))

	ev := sampleEvent()
	p.Process(ev, rec.Emitter())
	p.Process(ev, rec.Emitter()) // same key: dropped before the store is ever reached.

	if len(rec.LogRecords()) != 1 {
		t.Fatalf("log records = %d, want 1 (second call deduped)", len(rec.LogRecords()))
	}
	page := store.Page(eventstore.Query{Limit: 10})
	if len(page.Rows) != 1 {
		t.Fatalf("store rows = %d, want 1: a deduped event must not double-appear", len(page.Rows))
	}
}

// TestWithStore_PolicyDiffLongerThanCapIsTruncated is the negative test for
// #300's "policy-diff truncation" requirement: without it, a large
// policyUpdate-style old/new pair would retain the WHOLE document per event,
// which — held for up to the ring's capacity of events — defeats the "bounded
// memory" contract this package promises.
func TestWithStore_PolicyDiffLongerThanCapIsTruncated(t *testing.T) {
	rec := telemetrytest.New()
	store := eventstore.NewMemory(10)
	p := audit.NewProcessor(audit.WithStore(store))

	huge := strings.Repeat("x", eventstore.MaxDetailBytes*2)
	ev := sampleEvent()
	ev.New = json.RawMessage(`"` + huge + `"`)
	p.Process(ev, rec.Emitter())

	page := store.Page(eventstore.Query{Limit: 1})
	got := page.Rows[0]
	if !got.Truncated {
		t.Fatal("Truncated = false, want true for an oversized policy diff")
	}
	if len(got.Details) >= len(huge) {
		t.Errorf("stored details len = %d, want less than the original %d", len(got.Details), len(huge))
	}
}

// TestProcessCtxThreadsTraceContextOntoLog is the #367 acceptance test for the
// audit processor: a sampled ctx passed via ProcessCtx must produce a log
// record carrying the SAME native TraceID/SpanID, and Process (unchanged,
// ctx-free) must remain exactly as before — no trace context on its log.
func TestProcessCtxThreadsTraceContextOntoLog(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor()

	wantTraceID := trace.TraceID{0x11, 0x22}
	wantSpanID := trace.SpanID{0x33, 0x44}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     wantSpanID,
		TraceFlags: trace.FlagsSampled,
	}))

	p.ProcessCtx(ctx, sampleEvent(), rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("log records = %d, want 1", len(recs))
	}
	if recs[0].TraceID != wantTraceID.String() {
		t.Errorf("TraceID = %q, want %q", recs[0].TraceID, wantTraceID.String())
	}
	if recs[0].SpanID != wantSpanID.String() {
		t.Errorf("SpanID = %q, want %q", recs[0].SpanID, wantSpanID.String())
	}
}

// TestProcessAllCtxThreadsTraceContext is the ProcessAll-side analog: every
// event in the batch gets the same ctx's trace context.
func TestProcessAllCtxThreadsTraceContext(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor()

	wantTraceID := trace.TraceID{0x55, 0x66}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     trace.SpanID{0x77},
		TraceFlags: trace.FlagsSampled,
	}))

	ev1, ev2 := sampleEvent(), sampleEvent()
	ev2.Target.ID = "n2"
	p.ProcessAllCtx(ctx, audit.ConfigurationResponse{Logs: []audit.Event{ev1, ev2}}, rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 2 {
		t.Fatalf("log records = %d, want 2", len(recs))
	}
	for i, r := range recs {
		if r.TraceID != wantTraceID.String() {
			t.Errorf("record %d TraceID = %q, want %q", i, r.TraceID, wantTraceID.String())
		}
	}
}
