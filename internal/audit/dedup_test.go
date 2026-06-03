package audit_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// counterTotal sums the value across all data points of the audit events
// counter currently recorded in rec.
func counterTotal(rec *telemetrytest.Recorder) float64 {
	var total float64
	for _, mp := range rec.MetricPoints(audit.MetricAuditEvents) {
		total += mp.Value
	}
	return total
}

// TestProcessNoDedupEmitsDuplicates confirms back-compat: with no dedup set
// configured (NewProcessor() with no args), feeding the same event twice emits
// two log records and a counter total of 2.
func TestProcessNoDedupEmitsDuplicates(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor()

	ev := sampleEvent()
	p.Process(ev, rec.Emitter())
	p.Process(ev, rec.Emitter())

	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("log records = %d, want 2", got)
	}
	if got := counterTotal(rec); got != 2 {
		t.Fatalf("counter total = %v, want 2", got)
	}
}

// TestProcessSharedDedupSuppressesDuplicate confirms that with a shared dedup
// set, feeding the same event twice emits exactly one log record and a counter
// total of 1 (the second is suppressed entirely).
func TestProcessSharedDedupSuppressesDuplicate(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	p := audit.NewProcessor(audit.WithDedup(set))

	ev := sampleEvent()
	p.Process(ev, rec.Emitter())
	p.Process(ev, rec.Emitter())

	if got := len(rec.LogRecords()); got != 1 {
		t.Fatalf("log records = %d, want 1", got)
	}
	if got := counterTotal(rec); got != 1 {
		t.Fatalf("counter total = %v, want 1", got)
	}
}

// TestProcessSharedDedupDistinctGroupsBothEmit confirms two events with
// different eventGroupID values are treated as distinct and both emit.
func TestProcessSharedDedupDistinctGroupsBothEmit(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	p := audit.NewProcessor(audit.WithDedup(set))

	a := sampleEvent()
	a.EventGroupID = "g-a"
	b := sampleEvent()
	b.EventGroupID = "g-b"

	p.Process(a, rec.Emitter())
	p.Process(b, rec.Emitter())

	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("log records = %d, want 2", got)
	}
	if got := counterTotal(rec); got != 2 {
		t.Fatalf("counter total = %v, want 2", got)
	}
}

// TestProcessSharedDedupNoGroupDisambiguatesByActionTarget confirms that when
// the eventGroupID is empty, two events sharing the same eventTime but with
// different Action/Target.ID are NOT collapsed (the fallback key disambiguates).
func TestProcessSharedDedupNoGroupDisambiguatesByActionTarget(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	p := audit.NewProcessor(audit.WithDedup(set))

	when := time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC)

	a := sampleEvent()
	a.EventGroupID = ""
	a.EventTime = when
	a.Action = "CREATE"
	a.Target.ID = "n1"

	b := sampleEvent()
	b.EventGroupID = ""
	b.EventTime = when
	b.Action = "DELETE"
	b.Target.ID = "n2"

	p.Process(a, rec.Emitter())
	p.Process(b, rec.Emitter())

	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("log records = %d, want 2", got)
	}
	if got := counterTotal(rec); got != 2 {
		t.Fatalf("counter total = %v, want 2", got)
	}
}

// TestProcessSharedDedupSamePropertyTwiceCollapses pins the ACCEPTED tradeoff of
// the time-free cross-source key (S4-9(2)): two events that share
// (eventGroupID, action, target.id, property) but occur at DIFFERENT times
// collapse to one in the processor's shared dedup set. This is the price of
// matching poll (ns eventTime) against stream (ms HEC time, no inner eventTime);
// such a pair should not arise within one logical operation, but the behavior is
// pinned so any future change to it is deliberate (D11). The auditlogs collector's
// time-based window-boundary dedup (eventKey) would instead keep both.
func TestProcessSharedDedupSamePropertyTwiceCollapses(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	p := audit.NewProcessor(audit.WithDedup(set))

	// sampleEvent: action CREATE, target.id n1, property ALLOWED_IPS.
	a := sampleEvent()
	a.EventGroupID = "g1"
	a.EventTime = time.Date(2026, 6, 3, 11, 30, 1, 100, time.UTC)
	b := sampleEvent()
	b.EventGroupID = "g1"
	b.EventTime = time.Date(2026, 6, 3, 11, 30, 2, 200, time.UTC) // different time, same identity

	p.Process(a, rec.Emitter())
	p.Process(b, rec.Emitter())

	if got := counterTotal(rec); got != 1 {
		t.Fatalf("counter total = %v, want 1 (same egid|action|target|property collapses despite different time)", got)
	}
}

// TestDedupKeyFormula pins the exported cross-source key formula. This is the key
// the PROCESSOR's poll<->stream de-dup set uses — distinct from the auditlogs
// collector's time-based window-boundary eventKey() (they are intentionally
// different; see DedupKey doc).
func TestDedupKeyFormula(t *testing.T) {
	when := time.Date(2026, 6, 2, 19, 0, 5, 558078907, time.UTC)
	t1 := when.UTC().Format(time.RFC3339Nano)

	// With an eventGroupID the key is the SOURCE-INDEPENDENT identity
	// eventGroupID|action|target.id|property (time-free) so it matches across the
	// poll and stream paths, whose timestamps differ in source/precision (S4-9(2)).
	withGroup := sampleEvent() // action CREATE, target.id n1, property ALLOWED_IPS
	withGroup.EventGroupID = "g-string"
	withGroup.EventTime = when
	if got, want := audit.DedupKey(withGroup), "g-string|CREATE|n1|ALLOWED_IPS"; got != want {
		t.Errorf("DedupKey(withGroup) = %q, want %q", got, want)
	}

	noGroup := sampleEvent()
	noGroup.EventGroupID = ""
	noGroup.EventTime = when
	noGroup.Action = "UPDATE"
	noGroup.Target.ID = "n9"
	if got, want := audit.DedupKey(noGroup), t1+"|UPDATE|n9"; got != want {
		t.Errorf("DedupKey(noGroup) = %q, want %q", got, want)
	}
}
