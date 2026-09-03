package flowstore_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

// #295: the aggregate Query already honors an [Start, End) window, but the
// connection ring had no equivalent — Recent always returned the global
// newest rows regardless of what window the caller pinned the aggregate to.
// A caller viewing a historical, pinned window (an operator scrubbing back in
// time) would see a historical chart next to a "recent connections" list
// still showing right-now traffic. RecentRange gives the store side of the
// fix: a bounded time-range recent query the API layer can point at the same
// Start/End it gives Query.

// The basic case: a window strictly inside the retained range must return
// only the rows whose event time falls inside it, newest first.
func TestMemory_RecentRangeFiltersToWindow(t *testing.T) {
	s := newMemory(0)
	s.Record(conn(base.Add(-10*time.Minute), "a:1", "b:2", 1))
	s.Record(conn(base.Add(-5*time.Minute), "a:1", "b:2", 2))
	s.Record(conn(base, "a:1", "b:2", 3))
	s.Record(conn(base.Add(5*time.Minute), "a:1", "b:2", 4))

	got := s.RecentRange(base.Add(-6*time.Minute), base.Add(time.Minute), 10)
	if len(got) != 2 {
		t.Fatalf("RecentRange returned %d entries, want 2 (the -5m and 0m rows): %+v", len(got), got)
	}
	if got[0].Counts.TxBytes != 3 || got[1].Counts.TxBytes != 2 {
		t.Errorf("order/content = %d, %d; want 3, 2 (newest first, in-window only)",
			got[0].Counts.TxBytes, got[1].Counts.TxBytes)
	}
}

// A window that does not overlap anything retained must return nothing, not
// fall back to "everything" or panic.
func TestMemory_RecentRangeEmptyForNonOverlappingWindow(t *testing.T) {
	s := newMemory(0)
	s.Record(conn(base, "a:1", "b:2", 1))

	got := s.RecentRange(base.Add(time.Hour), base.Add(2*time.Hour), 10)
	if len(got) != 0 {
		t.Errorf("RecentRange for a disjoint future window returned %d entries, want 0: %+v", len(got), got)
	}
}

// A window straddling the edge of what the ring still holds (some rows
// evicted by the MaxRecent cap) must return exactly what's both retained and
// in-window — not more, not fewer.
func TestMemory_RecentRangePartiallyRetained(t *testing.T) {
	s := newMemory(0)
	for i := range flowstore.MaxRecent + 100 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}
	// The oldest 100 records (tx 0..99) have been evicted from the ring.
	// Ask for a window covering tx 50..150 by time; only 100..150 remain.
	start := base.Add(50 * time.Second)
	end := base.Add(150 * time.Second)

	got := s.RecentRange(start, end, 1000)
	if len(got) != 51 { // 100..150 inclusive
		t.Fatalf("RecentRange returned %d entries, want 51 (only the still-retained half of the window): %+v",
			len(got), summarizeTx(got))
	}
	if got[0].Counts.TxBytes != 150 {
		t.Errorf("newest in window tx = %d, want 150", got[0].Counts.TxBytes)
	}
	if got[len(got)-1].Counts.TxBytes != 100 {
		t.Errorf("oldest retained in window tx = %d, want 100", got[len(got)-1].Counts.TxBytes)
	}
}

// A zero End means "now" (the store's clock), matching Query's convention, so
// live mode (no end pinned) keeps returning the true newest rows.
func TestMemory_RecentRangeZeroEndMeansNow(t *testing.T) {
	s := newMemory(0)
	s.Record(conn(base.Add(-time.Minute), "a:1", "b:2", 1))
	s.Record(conn(base, "a:1", "b:2", 2))

	got := s.RecentRange(time.Time{}, time.Time{}, 10)
	if len(got) != 2 {
		t.Fatalf("RecentRange with zero start/end returned %d entries, want 2", len(got))
	}
	if got[0].Counts.TxBytes != 2 {
		t.Errorf("newest tx = %d, want 2", got[0].Counts.TxBytes)
	}
}

// A non-positive limit must not be read as "everything", matching Recent's
// contract.
func TestMemory_RecentRangeLimits(t *testing.T) {
	s := newMemory(0)
	s.Record(conn(base, "a:1", "b:2", 1))

	if got := s.RecentRange(time.Time{}, time.Time{}, 0); got != nil {
		t.Errorf("RecentRange with limit 0 = %+v, want nil", got)
	}
	if got := s.RecentRange(time.Time{}, time.Time{}, -1); got != nil {
		t.Errorf("RecentRange with limit -1 = %+v, want nil", got)
	}
}

func summarizeTx(rows []flowstore.Recent) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.Counts.TxBytes
	}
	return out
}
