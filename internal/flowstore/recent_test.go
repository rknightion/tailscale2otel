package flowstore_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/flowstore"
)

// conn returns an observation carrying the raw endpoints a flow record supplies,
// which the aggregated dimensions deliberately discard.
func conn(at time.Time, srcAddr, dstAddr string, tx int64) flowstore.Observation {
	o := obs(at, "a", "b", tx, 0)
	o.SrcAddr, o.DstAddr = srcAddr, dstAddr
	return o
}

// The aggregate view answers "how much"; the flow list answers "what exactly".
// Individual connections are not recoverable from the minute buckets, so the
// store retains the most recent ones verbatim.
func TestMemory_RetainsRecentConnections(t *testing.T) {
	s := flowstore.NewMemory(0)
	s.Record(conn(base, "100.64.0.1:52000", "100.64.0.2:443", 120))

	got := s.Recent(10)
	if len(got) != 1 {
		t.Fatalf("Recent returned %d entries, want 1", len(got))
	}
	r := got[0]
	if r.SrcAddr != "100.64.0.1:52000" || r.DstAddr != "100.64.0.2:443" {
		t.Errorf("endpoints = %q -> %q, want the raw addresses the record carried", r.SrcAddr, r.DstAddr)
	}
	if r.SrcNode != "a" || r.DstNode != "b" {
		t.Errorf("nodes = %q -> %q, want a -> b", r.SrcNode, r.DstNode)
	}
	if r.Counts.TxBytes != 120 {
		t.Errorf("tx = %d, want 120", r.Counts.TxBytes)
	}
	if !r.Time.Equal(base) {
		t.Errorf("time = %v, want %v", r.Time, base)
	}
}

// Newest first: an operator opening the page wants what just happened, and the
// limit must cut the oldest rather than the newest.
func TestMemory_RecentIsNewestFirst(t *testing.T) {
	s := flowstore.NewMemory(0)
	for i := range 5 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}

	got := s.Recent(3)
	if len(got) != 3 {
		t.Fatalf("Recent(3) returned %d entries", len(got))
	}
	for i, want := range []int64{4, 3, 2} {
		if got[i].Counts.TxBytes != want {
			t.Errorf("entry %d tx = %d, want %d (wrong order or wrong end truncated)", i, got[i].Counts.TxBytes, want)
		}
	}
}

// The ring is the one unaggregated thing the store holds, so it is the one most
// at risk from a flood on the stream ingress. It must be bounded by count, not
// by time.
func TestMemory_RecentRingIsBounded(t *testing.T) {
	s := flowstore.NewMemory(0)
	for i := range flowstore.MaxRecent + 500 {
		s.Record(conn(base.Add(time.Duration(i)*time.Millisecond), "a:1", "b:2", int64(i)))
	}

	got := s.Recent(flowstore.MaxRecent * 2)
	if len(got) != flowstore.MaxRecent {
		t.Fatalf("Recent returned %d entries, want the ring cap %d", len(got), flowstore.MaxRecent)
	}
	// The newest survived; the oldest were overwritten.
	if want := int64(flowstore.MaxRecent + 499); got[0].Counts.TxBytes != want {
		t.Errorf("newest tx = %d, want %d", got[0].Counts.TxBytes, want)
	}
	if want := int64(500); got[len(got)-1].Counts.TxBytes != want {
		t.Errorf("oldest retained tx = %d, want %d", got[len(got)-1].Counts.TxBytes, want)
	}
}

// A non-positive limit must not be read as "everything" by accident, and must
// not panic.
func TestMemory_RecentLimits(t *testing.T) {
	s := flowstore.NewMemory(0)
	for i := range 10 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", 1))
	}
	if got := len(s.Recent(0)); got != 0 {
		t.Errorf("Recent(0) returned %d entries, want 0", got)
	}
	if got := len(s.Recent(-1)); got != 0 {
		t.Errorf("Recent(-1) returned %d entries, want 0", got)
	}
	if got := len(s.Recent(100)); got != 10 {
		t.Errorf("Recent(100) returned %d entries, want all 10", got)
	}
}

// An empty store is the first thing an operator sees on a fresh start.
func TestMemory_RecentOnEmptyStore(t *testing.T) {
	if got := flowstore.NewMemory(0).Recent(10); len(got) != 0 {
		t.Errorf("Recent on an empty store returned %d entries", len(got))
	}
}

// Retaining raw connections must not disturb the aggregate answers.
func TestMemory_RecentDoesNotAffectAggregates(t *testing.T) {
	s := flowstore.NewMemory(0)
	s.Record(conn(base, "a:1", "b:2", 100))
	s.Record(conn(base, "a:1", "b:2", 100))

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	if res.Totals.TxBytes != 200 {
		t.Errorf("total tx = %d, want 200", res.Totals.TxBytes)
	}
	if res.Totals.Flows != 2 {
		t.Errorf("flows = %d, want 2", res.Totals.Flows)
	}
}
