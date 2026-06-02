package audit_test

import (
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
	if !strings.Contains(lr.Body, "a@example.com") {
		t.Fatalf("body %q does not contain login a@example.com", lr.Body)
	}
	if !strings.Contains(lr.Body, "CREATE") {
		t.Fatalf("body %q does not contain action CREATE", lr.Body)
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
	ev.Old = "1.2.3.4/32"
	ev.New = "5.6.7.8/32"
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
