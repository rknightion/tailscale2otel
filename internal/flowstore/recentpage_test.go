package flowstore_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// #296: RecentRange returns at most maxFlowsRecent rows, and the browser
// filtered only that returned tail — a connection matching the operator's
// filter but sitting outside the returned window was invisible. RecentPage
// moves filtering server-side and adds cursor pagination so a filtered search
// can walk the whole ring rather than the last page of it.

// full sets every field RecentFilter can match against, so a single fixture
// can exercise every filter dimension independently.
func full(at time.Time, tx int64) flowstore.Observation {
	o := obs(at, "camden", "mbp16", tx, 0)
	o.SrcAddr, o.DstAddr = "100.64.0.1:1", "100.64.0.2:443"
	o.DstService = "https"
	o.SrcUser, o.DstUser = "rob@example.com", "amy@example.com"
	o.SrcTags, o.DstTags = "tag:servers", "tag:laptops"
	o.TrafficType = "virtual"
	o.Verdict = "permitted"
	o.Path = "direct"
	return o
}

// Baseline: an empty filter and Cursor 0 must return exactly what RecentRange
// returns, in the same order — RecentPage is a superset, not a replacement,
// and every existing caller of RecentRange must see no behavior change.
func TestMemory_RecentPageEmptyFilterMatchesRecentRange(t *testing.T) {
	s := newMemory(0)
	for i := range 20 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}

	start, end := base.Add(-time.Minute), base.Add(time.Minute)
	want := s.RecentRange(start, end, 7)
	page := s.RecentPage(flowstore.RecentQuery{Start: start, End: end, Limit: 7})

	if len(page.Rows) != len(want) {
		t.Fatalf("RecentPage returned %d rows, RecentRange returned %d", len(page.Rows), len(want))
	}
	for i := range want {
		if page.Rows[i].Time != want[i].Time || page.Rows[i].Counts.TxBytes != want[i].Counts.TxBytes {
			t.Fatalf("row %d differs: RecentPage=%+v RecentRange=%+v", i, page.Rows[i], want[i])
		}
	}
}

// Same equivalence, but exercised against the partially-retained/wrapped ring
// case RecentRange has its own dedicated test for (TestMemory_RecentRangePartiallyRetained).
func TestMemory_RecentPageEmptyFilterMatchesRecentRangePartiallyRetained(t *testing.T) {
	s := newMemory(0)
	for i := range flowstore.MaxRecent + 100 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}
	start := base.Add(50 * time.Second)
	end := base.Add(150 * time.Second)

	want := s.RecentRange(start, end, 1000)
	page := s.RecentPage(flowstore.RecentQuery{Start: start, End: end, Limit: 1000})

	if len(page.Rows) != len(want) {
		t.Fatalf("RecentPage returned %d rows, RecentRange returned %d", len(page.Rows), len(want))
	}
	for i := range want {
		if page.Rows[i].Counts.TxBytes != want[i].Counts.TxBytes {
			t.Fatalf("row %d tx = %d, want %d", i, page.Rows[i].Counts.TxBytes, want[i].Counts.TxBytes)
		}
	}
}

// A non-positive limit returns no rows, matching Recent/RecentRange's
// contract — a caller's page size is never read as "everything".
func TestMemory_RecentPageNonPositiveLimitReturnsNothing(t *testing.T) {
	s := newMemory(0)
	s.Record(conn(base, "a:1", "b:2", 1))

	for _, limit := range []int{0, -1} {
		page := s.RecentPage(flowstore.RecentQuery{Limit: limit})
		if page.Rows != nil {
			t.Errorf("limit %d: Rows = %+v, want nil", limit, page.Rows)
		}
	}
}

// The core of #296: a filter matching a row OUTSIDE the returned page must
// still be found via NextCursor, not silently dropped because it fell past
// the first page's Limit.
func TestMemory_RecentPageFiltersAcrossTheWholeRing(t *testing.T) {
	s := newMemory(0)
	// 50 rows; only the very first (oldest, so last on a small page) matches.
	s.Record(func() flowstore.Observation {
		o := conn(base, "a:1", "b:2", 999)
		o.SrcNode = "needle"
		return o
	}())
	for i := 1; i < 50; i++ {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}

	page := s.RecentPage(flowstore.RecentQuery{
		End:    base.Add(time.Hour), // newMemory's clock is fixed at base; the decoys are "after now"
		Limit:  5,
		Filter: flowstore.RecentFilter{Device: "needle"},
	})
	if len(page.Rows) != 1 || page.Rows[0].SrcNode != "needle" {
		t.Fatalf("RecentPage.Rows = %+v, want exactly the needle row", page.Rows)
	}
	if page.Matched != 1 {
		t.Errorf("Matched = %d, want 1", page.Matched)
	}
	if page.NextCursor != 0 {
		t.Errorf("NextCursor = %d, want 0 (nothing further matches)", page.NextCursor)
	}
}

// Matched counts every in-window match, ignoring both Limit and Cursor — it
// answers "how many are there", not "how many did this page return".
func TestMemory_RecentPageMatchedIgnoresLimitAndCursor(t *testing.T) {
	s := newMemory(0)
	for i := range 10 {
		o := conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i))
		o.SrcNode = "watched"
		s.Record(o)
	}

	page := s.RecentPage(flowstore.RecentQuery{
		End:    base.Add(time.Hour), // newMemory's clock is fixed at base; the fixture's events are "after now"
		Limit:  2,
		Cursor: 0,
		Filter: flowstore.RecentFilter{Device: "watched"},
	})
	if page.Matched != 10 {
		t.Errorf("Matched = %d, want 10 (all in-window matches, regardless of the page size)", page.Matched)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("Rows = %d, want the requested page size of 2", len(page.Rows))
	}
}

// Cursor-driven "load more": walking pages via NextCursor must eventually
// enumerate every matching row exactly once, oldest row last.
func TestMemory_RecentPageCursorPaginatesWithoutGapsOrDuplicates(t *testing.T) {
	s := newMemory(0)
	for i := range 25 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}

	var got []int64
	var cursor uint64
	for pages := 0; pages < 100; pages++ {
		page := s.RecentPage(flowstore.RecentQuery{End: base.Add(time.Hour), Limit: 4, Cursor: cursor})
		for _, r := range page.Rows {
			got = append(got, r.Counts.TxBytes)
		}
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
	}

	if len(got) != 25 {
		t.Fatalf("paginated through %d rows, want all 25: %v", len(got), got)
	}
	seen := map[int64]bool{}
	for i, tx := range got {
		if seen[tx] {
			t.Fatalf("tx %d returned more than once across pages: %v", tx, got)
		}
		seen[tx] = true
		if i > 0 && got[i-1] < tx {
			t.Fatalf("pages are not newest-first across the boundary: %v", got)
		}
	}
	for tx := int64(0); tx < 25; tx++ {
		if !seen[tx] {
			t.Errorf("tx %d was never returned by any page", tx)
		}
	}
}

// A Cursor from a restarted process (or simply stale) that no longer
// corresponds to anything retained must fail safe to "from the newest",
// never to an error or an empty page — see the API layer's contract for
// malformed cursors.
func TestMemory_RecentPageUnknownCursorStartsFromNewest(t *testing.T) {
	s := newMemory(0)
	for i := range 5 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}
	page := s.RecentPage(flowstore.RecentQuery{End: base.Add(time.Hour), Limit: 10, Cursor: 999999})
	if len(page.Rows) != 5 {
		t.Fatalf("rows with an out-of-range cursor = %d, want all 5 (cursor >= every seq is a no-op filter)",
			len(page.Rows))
	}
}

// Retained reports the ring's current occupancy regardless of the requested
// window or filter, so the page can distinguish "nothing matches" from "the
// ring holds less than you'd expect".
func TestMemory_RecentPageRetainedIgnoresWindowAndFilter(t *testing.T) {
	s := newMemory(0)
	for i := range 12 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}
	page := s.RecentPage(flowstore.RecentQuery{
		Start:  base.Add(time.Hour), // disjoint window: no matches
		End:    base.Add(2 * time.Hour),
		Limit:  10,
		Filter: flowstore.RecentFilter{Device: "nothing-matches-this"},
	})
	if page.Retained != 12 {
		t.Errorf("Retained = %d, want 12 (the ring's occupancy, independent of window/filter)", page.Retained)
	}
	if page.Matched != 0 {
		t.Errorf("Matched = %d, want 0", page.Matched)
	}
}

// Truncated tells the operator the ring has evicted rows, so "no more
// matches" (nothing further exists) can be told apart from "the ring no
// longer holds them" (evicted before this query ever ran).
func TestMemory_RecentPageTruncatedWhenRingIsFull(t *testing.T) {
	s := newMemory(0)
	for i := range flowstore.MaxRecent - 1 {
		s.Record(conn(base.Add(time.Duration(i)*time.Second), "a:1", "b:2", int64(i)))
	}
	if got := s.RecentPage(flowstore.RecentQuery{Limit: 1}); got.Truncated {
		t.Error("Truncated = true with the ring one short of MaxRecent")
	}
	s.Record(conn(base.Add(time.Duration(flowstore.MaxRecent-1)*time.Second), "a:1", "b:2", 999))
	if got := s.RecentPage(flowstore.RecentQuery{Limit: 1}); !got.Truncated {
		t.Error("Truncated = false with the ring at MaxRecent")
	}
}

// Every RecentFilter dimension, checked independently against a fixture that
// carries a distinct value in every matchable field.
func TestMemory_RecentPageFilterDimensions(t *testing.T) {
	s := newMemory(0)
	s.Record(full(base, 1))
	// The decoy differs on every dimension full() sets, including the ones
	// obs()'s defaults would otherwise share with the fixture (DstService and
	// TrafficType) — otherwise a filter that (wrongly) matched everything
	// could still pass by accident via the decoy incidentally qualifying too.
	s.Record(func() flowstore.Observation {
		o := conn(base.Add(time.Second), "z:1", "y:2", 2)
		o.DstService = "ssh"
		o.TrafficType = "subnet"
		o.Verdict = "no_rule"
		o.Path = "derp"
		return o
	}())

	tests := []struct {
		name   string
		filter flowstore.RecentFilter
	}{
		{"device matches src, case-insensitive substring", flowstore.RecentFilter{Device: "CAMD"}},
		{"device matches dst", flowstore.RecentFilter{Device: "mbp16"}},
		{"addr matches src", flowstore.RecentFilter{Addr: "100.64.0.1"}},
		{"addr matches dst", flowstore.RecentFilter{Addr: "0.2:443"}},
		{"service substring", flowstore.RecentFilter{Service: "HTTP"}},
		{"identity matches src user", flowstore.RecentFilter{Identity: "rob@"}},
		{"identity matches dst user", flowstore.RecentFilter{Identity: "amy@"}},
		{"identity matches src tags", flowstore.RecentFilter{Identity: "servers"}},
		{"identity matches dst tags", flowstore.RecentFilter{Identity: "laptops"}},
		{"type exact, case-insensitive", flowstore.RecentFilter{Type: "VIRTUAL"}},
		{"verdict exact, case-insensitive", flowstore.RecentFilter{Verdict: "Permitted"}},
		{"path exact, case-insensitive", flowstore.RecentFilter{Path: "Direct"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page := s.RecentPage(flowstore.RecentQuery{End: base.Add(time.Hour), Limit: 10, Filter: tc.filter})
			if len(page.Rows) != 1 || page.Rows[0].Counts.TxBytes != 1 {
				t.Fatalf("filter %+v matched %d rows, want exactly the fixture row: %+v", tc.filter, len(page.Rows), page.Rows)
			}
		})
	}
}

// Type/Verdict/Path are exact matches (not substring): a filter of "perm"
// must not match "permitted" the way a substring filter would, because these
// are closed enumerations where "perm" is not a value anyone would type
// expecting a partial match, and treating them as substrings would make
// "no" match both "no_rule" and everything containing "no".
func TestMemory_RecentPageExactFieldsDoNotSubstringMatch(t *testing.T) {
	s := newMemory(0)
	s.Record(full(base, 1))

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Type: "virt"}})
	if len(page.Rows) != 0 {
		t.Errorf("Type=\"virt\" matched %d rows against traffic_type \"virtual\"; Type must be an exact match", len(page.Rows))
	}
}

// Two filter dimensions both set is an AND, not an OR: a row must satisfy
// every non-empty field to match.
func TestMemory_RecentPageFilterDimensionsAreANDed(t *testing.T) {
	s := newMemory(0)
	s.Record(full(base, 1))
	s.Record(conn(base.Add(time.Second), "camden", "someone-else", 2))

	page := s.RecentPage(flowstore.RecentQuery{
		End:    base.Add(time.Hour),
		Limit:  10,
		Filter: flowstore.RecentFilter{Device: "camden", Service: "https"},
	})
	if len(page.Rows) != 1 || page.Rows[0].Counts.TxBytes != 1 {
		t.Fatalf("AND of two filters matched %+v, want exactly the row satisfying both", page.Rows)
	}
}

// A window boundary combined with a filter: rows outside [Start, End] must
// never appear even if they'd otherwise match.
func TestMemory_RecentPageWindowAndFilterCombine(t *testing.T) {
	s := newMemory(0)
	s.Record(func() flowstore.Observation {
		o := conn(base.Add(-time.Hour), "a:1", "b:2", 1)
		o.SrcNode = "outside-window"
		return o
	}())
	s.Record(func() flowstore.Observation {
		o := conn(base, "a:1", "b:2", 2)
		o.SrcNode = "outside-window"
		return o
	}())

	page := s.RecentPage(flowstore.RecentQuery{
		Start:  base.Add(-time.Minute),
		End:    base.Add(time.Minute),
		Limit:  10,
		Filter: flowstore.RecentFilter{Device: "outside-window"},
	})
	if len(page.Rows) != 1 || page.Rows[0].Counts.TxBytes != 2 {
		t.Fatalf("window did not exclude the out-of-window match: %+v", page.Rows)
	}
}
