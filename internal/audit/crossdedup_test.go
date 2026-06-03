package audit_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestNormalizedCrossKey pins the shared cross-source key FORMAT. This is the
// single home for the formula both the audit Processor and the webhook Server
// use to reconcile an audit event and a webhook event that describe the same
// change. Any empty component yields ok=false so callers skip cross-dedup rather
// than risk collapsing unrelated events.
func TestNormalizedCrossKey(t *testing.T) {
	when := time.Date(2024, 6, 6, 15, 25, 26, 123456789, time.UTC)
	got, ok := audit.NormalizedCrossKey("create", "node", "n1", when)
	if !ok {
		t.Fatalf("NormalizedCrossKey ok = false, want true")
	}
	// Sub-second precision is intentionally bucketed away (truncated to the second).
	if want := "xsrc|create|node|n1|2024-06-06T15:25:26Z"; got != want {
		t.Fatalf("NormalizedCrossKey = %q, want %q", got, want)
	}

	for _, c := range []struct{ verb, subj, id string }{
		{"", "node", "n1"},
		{"create", "", "n1"},
		{"create", "node", ""},
	} {
		if _, ok := audit.NormalizedCrossKey(c.verb, c.subj, c.id, when); ok {
			t.Errorf("NormalizedCrossKey(%q,%q,%q) ok = true, want false (incomplete)", c.verb, c.subj, c.id)
		}
	}
}

// TestCrossSourceKey confirms an audit Event maps onto the canonical
// (lowercased action, lowercased target type, target id) vocabulary, and that a
// missing target id makes it un-keyable.
func TestCrossSourceKey(t *testing.T) {
	ev := sampleEvent() // Action CREATE, Target.Type NODE, Target.ID n1
	got, ok := audit.CrossSourceKey(ev)
	if !ok {
		t.Fatalf("CrossSourceKey ok = false, want true")
	}
	want, _ := audit.NormalizedCrossKey("create", "node", "n1", ev.EventTime)
	if got != want {
		t.Fatalf("CrossSourceKey = %q, want %q", got, want)
	}

	ev.Target.ID = ""
	if _, ok := audit.CrossSourceKey(ev); ok {
		t.Errorf("CrossSourceKey with empty target id ok = true, want false")
	}
}

// TestWithCrossDedupSuppressesSameChange confirms that with a cross-source dedup
// set, a second audit event mapping to the same cross-key is suppressed even
// though it has a DIFFERENT eventGroupID (so the existing eventGroupID dedup
// would not have caught it).
func TestWithCrossDedupSuppressesSameChange(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	p := audit.NewProcessor(audit.WithCrossDedup(set))

	a := sampleEvent()
	a.EventGroupID = "g-a"
	b := sampleEvent()
	b.EventGroupID = "g-b" // distinct group: only the cross-key collapses them

	p.Process(a, rec.Emitter())
	p.Process(b, rec.Emitter())

	if got := len(rec.LogRecords()); got != 1 {
		t.Fatalf("log records = %d, want 1 (cross-dedup)", got)
	}
	if got := counterTotal(rec); got != 1 {
		t.Fatalf("counter total = %v, want 1 (cross-dedup)", got)
	}
}

// TestWithCrossDedupDistinctChangesBothEmit confirms different targets are not
// collapsed by the cross-source set.
func TestWithCrossDedupDistinctChangesBothEmit(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	p := audit.NewProcessor(audit.WithCrossDedup(set))

	a := sampleEvent()
	a.Target.ID = "n1"
	b := sampleEvent()
	b.Target.ID = "n2"

	p.Process(a, rec.Emitter())
	p.Process(b, rec.Emitter())

	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("log records = %d, want 2 (distinct targets)", got)
	}
}
