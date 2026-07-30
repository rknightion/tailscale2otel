package sqlitestore

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
)

// openRecentTestStore opens a Store rooted in a fresh t.TempDir(), with any
// per-test override applied before Open. writer.go (owned by another lane)
// is currently a stub that persists nothing, so every fixture here goes in
// via insertFlow below rather than Record/RecordResult.
//
// Named distinctly from writer_test.go's own openTestStore/testOpts (a
// sibling file owned by another lane, editing concurrently) to avoid a
// redeclaration collision rather than touching a file this lane doesn't own.
func openRecentTestStore(t *testing.T, mutate func(*Options)) *Store {
	t.Helper()
	opts := Options{
		Dir:     t.TempDir(),
		Tailnet: "test",
	}
	if mutate != nil {
		mutate(&opts)
	}
	s, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// insertFlow inserts one raw row, built from the package's own `columns`
// slice so the fixture can never silently drift from what RecentPage
// actually selects. Any column not named in vals gets a zero-ish default
// matching the DDL's own DEFAULT (schema.go), except "rule" which defaults
// to -1 (a row that matched no rule) to match the real writer's convention.
func insertFlow(t *testing.T, s *Store, vals map[string]any) {
	t.Helper()
	if _, ok := vals["time"]; !ok {
		t.Fatalf("insertFlow: fixture must set time")
	}
	binds := make([]any, len(columns))
	placeholders := make([]string, len(columns))
	for i, c := range columns {
		placeholders[i] = "?"
		if v, ok := vals[c]; ok {
			binds[i] = v
			continue
		}
		switch c {
		case "rule":
			binds[i] = -1
		case "reversed", "tx_bytes", "rx_bytes", "tx_packets", "rx_packets", "flows":
			binds[i] = 0
		default:
			binds[i] = ""
		}
	}
	q := "INSERT INTO flows (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if _, err := s.db.Exec(q, binds...); err != nil {
		t.Fatalf("insertFlow: %v", err)
	}
}

var testBase = time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)

// at returns testBase + n minutes, the fixture clock every test below uses.
func at(n int) time.Time { return testBase.Add(time.Duration(n) * time.Minute) }

func TestRecentPage_EmptyStoreReturnsZeroPage(t *testing.T) {
	s := openRecentTestStore(t, nil)
	page := s.RecentPage(flowstore.RecentQuery{Limit: 10})
	if len(page.Rows) != 0 || page.Matched != 0 || page.Retained != 0 || page.NextCursor != 0 || page.Truncated {
		t.Fatalf("empty store: got %+v", page)
	}
}

func TestRecentPage_NewestFirstOrdering(t *testing.T) {
	s := openRecentTestStore(t, nil)
	for i := range 5 {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n"})
	}
	page := s.RecentPage(flowstore.RecentQuery{Limit: 10})
	if len(page.Rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(page.Rows))
	}
	for i, want := range []int{4, 3, 2, 1, 0} {
		if !page.Rows[i].Time.Equal(at(want)) {
			t.Fatalf("row %d: want time %v, got %v", i, at(want), page.Rows[i].Time)
		}
	}
}

func TestRecentPage_SeqTiebreakOnEqualTime(t *testing.T) {
	s := openRecentTestStore(t, nil)
	// Same timestamp, inserted in order; the later insert (higher seq) must
	// sort first among ties, matching Memory's "later ingested sorts newer".
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "src_node": "first"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "src_node": "second"})
	page := s.RecentPage(flowstore.RecentQuery{Limit: 10})
	if len(page.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(page.Rows))
	}
	if page.Rows[0].SrcNode != "second" || page.Rows[1].SrcNode != "first" {
		t.Fatalf("want [second, first], got [%s, %s]", page.Rows[0].SrcNode, page.Rows[1].SrcNode)
	}
}

func TestRecentPage_WindowIsInclusiveBothEnds(t *testing.T) {
	s := openRecentTestStore(t, nil)
	for i := range 5 {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n"})
	}
	page := s.RecentPage(flowstore.RecentQuery{Start: at(1), End: at(3), Limit: 10})
	if len(page.Rows) != 3 {
		t.Fatalf("want 3 rows in [1,3], got %d", len(page.Rows))
	}
	for i, want := range []int{3, 2, 1} {
		if !page.Rows[i].Time.Equal(at(want)) {
			t.Fatalf("row %d: want time %v, got %v", i, at(want), page.Rows[i].Time)
		}
	}
	if page.Matched != 3 {
		t.Fatalf("Matched: want 3, got %d", page.Matched)
	}
	if page.Retained != 5 {
		t.Fatalf("Retained: want 5 (ignores window), got %d", page.Retained)
	}
}

func TestRecentPage_ZeroEndUsesNow(t *testing.T) {
	now := at(10)
	s := openRecentTestStore(t, func(o *Options) { o.Now = func() time.Time { return now } })
	insertFlow(t, s, map[string]any{"time": timeToDB(at(5)), "src_node": "in-window"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(20)), "src_node": "future"})
	page := s.RecentPage(flowstore.RecentQuery{Limit: 10})
	if len(page.Rows) != 1 || page.Rows[0].SrcNode != "in-window" {
		t.Fatalf("want only the row at/before now, got %+v", page.Rows)
	}
}

func TestRecentPage_CursorPaginationWalksAllRowsOnceEach(t *testing.T) {
	s := openRecentTestStore(t, nil)
	const n = 25
	for i := range n {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n"})
	}

	seen := map[time.Time]bool{}
	var cursor uint64
	pages := 0
	for {
		page := s.RecentPage(flowstore.RecentQuery{Limit: 7, Cursor: cursor})
		if page.Matched != n {
			t.Fatalf("page %d: Matched want %d, got %d", pages, n, page.Matched)
		}
		for _, r := range page.Rows {
			if seen[r.Time] {
				t.Fatalf("row at %v served twice", r.Time)
			}
			seen[r.Time] = true
		}
		pages++
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
		if pages > n {
			t.Fatalf("pagination did not terminate")
		}
	}
	if len(seen) != n {
		t.Fatalf("want %d distinct rows walked, got %d (gap in pagination)", n, len(seen))
	}
	if pages != 4 { // 25 rows / 7 per page = 4 pages (7,7,7,4)
		t.Fatalf("want 4 pages, got %d", pages)
	}
}

func TestRecentPage_NextCursorZeroOnLastPage(t *testing.T) {
	s := openRecentTestStore(t, nil)
	for i := range 3 {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n"})
	}
	page := s.RecentPage(flowstore.RecentQuery{Limit: 10})
	if page.NextCursor != 0 {
		t.Fatalf("last (only) page: want NextCursor 0, got %d", page.NextCursor)
	}
}

func TestRecentPage_LimitClampedByMaxExportRows(t *testing.T) {
	s := openRecentTestStore(t, func(o *Options) { o.MaxExportRows = 3 })
	for i := range 10 {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n"})
	}
	page := s.RecentPage(flowstore.RecentQuery{Limit: 1000})
	if len(page.Rows) != 3 {
		t.Fatalf("want clamped to MaxExportRows=3, got %d", len(page.Rows))
	}
	if page.NextCursor == 0 {
		t.Fatalf("more rows remain beyond the clamp, want a non-zero NextCursor")
	}
}

func TestRecentPage_NonPositiveLimitReturnsNoRows(t *testing.T) {
	s := openRecentTestStore(t, nil)
	for i := range 3 {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n"})
	}
	for _, limit := range []int{0, -1} {
		page := s.RecentPage(flowstore.RecentQuery{Limit: limit})
		if page.Rows != nil {
			t.Fatalf("limit %d: want nil Rows, got %v", limit, page.Rows)
		}
		if page.Matched != 3 {
			t.Fatalf("limit %d: Matched should still be computed, got %d", limit, page.Matched)
		}
		if page.NextCursor != 0 {
			t.Fatalf("limit %d: want NextCursor 0, got %d", limit, page.NextCursor)
		}
	}
}

func TestRecentPage_Truncated(t *testing.T) {
	s := openRecentTestStore(t, func(o *Options) { o.MaxRows = 3 })
	for i := range 3 {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n"})
	}
	page := s.RecentPage(flowstore.RecentQuery{Limit: 10})
	if !page.Truncated {
		t.Fatalf("Retained (3) >= MaxRows (3): want Truncated true")
	}
}

func TestRecentPage_MatchedVsRetainedVsPageLength(t *testing.T) {
	s := openRecentTestStore(t, nil)
	for i := range 10 {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n", "verdict": flowstore.VerdictNoRule})
	}
	for i := 10; i < 14; i++ {
		insertFlow(t, s, map[string]any{"time": timeToDB(at(i)), "src_node": "n", "verdict": flowstore.VerdictPermitted})
	}
	page := s.RecentPage(flowstore.RecentQuery{
		Limit:  2,
		Filter: flowstore.RecentFilter{Verdict: flowstore.VerdictNoRule},
	})
	if page.Retained != 14 {
		t.Fatalf("Retained: want 14 (ignores filter), got %d", page.Retained)
	}
	if page.Matched != 10 {
		t.Fatalf("Matched: want 10 (filtered, ignores Limit), got %d", page.Matched)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("Rows: want 2 (Limit), got %d", len(page.Rows))
	}
}

// --- Filter field coverage, matching flowstore.RecentFilter.matches's exact
// column sets: Device -> src_node/dst_node, Addr -> src_addr/dst_addr,
// Service -> dst_service, Identity -> src_user/dst_user/src_tags/dst_tags,
// Type/Verdict/Path -> exact single columns.

func TestRecentPage_FilterDeviceMatchesEitherEnd(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "src_node": "workstation-a"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(1)), "dst_node": "workstation-b"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(2)), "src_node": "unrelated"})

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Device: "workstation"}})
	if page.Matched != 2 {
		t.Fatalf("Device filter: want 2 matches, got %d", page.Matched)
	}
}

func TestRecentPage_FilterAddrMatchesEitherEnd(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "src_addr": "100.64.0.1:443"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(1)), "dst_addr": "100.64.0.2:80"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(2)), "src_addr": "10.0.0.1:22"})

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Addr: "100.64.0"}})
	if page.Matched != 2 {
		t.Fatalf("Addr filter: want 2 matches, got %d", page.Matched)
	}
}

func TestRecentPage_FilterServiceMatchesDstServiceOnly(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "dst_service": "https"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(1)), "dst_service": "ssh"})

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Service: "http"}})
	if page.Matched != 1 {
		t.Fatalf("Service filter: want 1 match, got %d", page.Matched)
	}
}

func TestRecentPage_FilterIdentityMatchesAllFourColumns(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "src_user": "alice@example.com"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(1)), "dst_user": "alice@example.com"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(2)), "src_tags": "tag:alice-laptop"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(3)), "dst_tags": "tag:alice-laptop"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(4)), "src_user": "bob@example.com"})

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Identity: "alice"}})
	if page.Matched != 4 {
		t.Fatalf("Identity filter: want 4 matches, got %d", page.Matched)
	}
}

func TestRecentPage_FilterTypeExactMatch(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "traffic_type": "physical"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(1)), "traffic_type": "virtual"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(2)), "traffic_type": "physical-extra"})

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Type: "physical"}})
	if page.Matched != 1 {
		t.Fatalf("Type filter: want exact match only (1), got %d", page.Matched)
	}
}

func TestRecentPage_FilterVerdictExactMatch(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "verdict": flowstore.VerdictNoRule})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(1)), "verdict": flowstore.VerdictPermitted})

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Verdict: flowstore.VerdictNoRule}})
	if page.Matched != 1 {
		t.Fatalf("Verdict filter: want 1 match, got %d", page.Matched)
	}
}

func TestRecentPage_FilterPathExactMatch(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "path": flowstore.PathDirectIPv4})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(1)), "path": flowstore.PathDERP})

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Path: flowstore.PathDERP}})
	if page.Matched != 1 {
		t.Fatalf("Path filter: want 1 match, got %d", page.Matched)
	}
}

func TestRecentPage_FilterCaseInsensitive(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "src_node": "Workstation-A", "traffic_type": "Physical"})

	sub := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Device: "WORKSTATION"}})
	if sub.Matched != 1 {
		t.Fatalf("substring case-insensitivity: want 1 match, got %d", sub.Matched)
	}
	exact := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Type: "PHYSICAL"}})
	if exact.Matched != 1 {
		t.Fatalf("exact-match case-insensitivity: want 1 match, got %d", exact.Matched)
	}
}

// TestRecentPage_FilterEscapesLikeWildcards is the load-bearing one: a filter
// value containing a literal '%' or '_' must match ONLY the literal
// character, not act as a SQL LIKE wildcard — otherwise a device name or
// address containing either character would silently over-match.
func TestRecentPage_FilterEscapesLikeWildcards(t *testing.T) {
	s := openRecentTestStore(t, nil)
	insertFlow(t, s, map[string]any{"time": timeToDB(at(0)), "src_node": "host%literal"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(1)), "src_node": "hostXliteral"}) // would match "%" as wildcard
	insertFlow(t, s, map[string]any{"time": timeToDB(at(2)), "src_node": "host_literal"})
	insertFlow(t, s, map[string]any{"time": timeToDB(at(3)), "src_node": "hostYliteral"}) // would match "_" as wildcard

	pctPage := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Device: "host%literal"}})
	if pctPage.Matched != 1 {
		t.Fatalf("'%%' filter: want exactly 1 literal match, got %d", pctPage.Matched)
	}
	underscorePage := s.RecentPage(flowstore.RecentQuery{Limit: 10, Filter: flowstore.RecentFilter{Device: "host_literal"}})
	if underscorePage.Matched != 1 {
		t.Fatalf("'_' filter: want exactly 1 literal match, got %d", underscorePage.Matched)
	}
}
