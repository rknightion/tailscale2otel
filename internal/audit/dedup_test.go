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

// TestDedupKeyMatchesCollectorFormula pins the exported key formula to the exact
// shape the auditlogs collector relies on.
func TestDedupKeyMatchesCollectorFormula(t *testing.T) {
	when := time.Date(2026, 6, 2, 19, 0, 5, 558078907, time.UTC)
	t1 := when.UTC().Format(time.RFC3339Nano)

	withGroup := sampleEvent()
	withGroup.EventGroupID = "g-string"
	withGroup.EventTime = when
	if got, want := audit.DedupKey(withGroup), "g-string|"+t1; got != want {
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
