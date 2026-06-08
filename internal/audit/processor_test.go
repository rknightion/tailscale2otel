package audit_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
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
	// attributes (tailscale.actor.login, tailscale.target.name) where they are
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
		"enduser.id":                     "u1",
		"tailscale.actor.login":          "a@example.com",
		"tailscale.actor.display":        "Lion",
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
	// No error => no "error" attr, and no old/new since they were empty.
	if _, ok := lr.Attrs["error"]; ok {
		t.Errorf("unexpected error attr present: %q", lr.Attrs["error"])
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
	for _, k := range []string{"enduser.id", "tailscale.actor.login", "tailscale.target.id", "tailscale.target.name"} {
		if _, ok := mp.Attrs[k]; ok {
			t.Errorf("metric should not carry high-cardinality attr %q (=%q)", k, mp.Attrs[k])
		}
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
	if lr.Attrs["error"] != "permission denied" {
		t.Fatalf("error attr = %q, want permission denied", lr.Attrs["error"])
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
