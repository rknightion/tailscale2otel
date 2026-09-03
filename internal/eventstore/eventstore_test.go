package eventstore_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/eventstore"
)

func TestNewMemory_NonPositiveCapacitySelectsDefault(t *testing.T) {
	m := eventstore.NewMemory(0)
	if got := m.Stats().Capacity; got != eventstore.DefaultCapacity {
		t.Errorf("capacity = %d, want default %d", got, eventstore.DefaultCapacity)
	}
	m = eventstore.NewMemory(-5)
	if got := m.Stats().Capacity; got != eventstore.DefaultCapacity {
		t.Errorf("capacity(-5) = %d, want default %d", got, eventstore.DefaultCapacity)
	}
}

func TestRecordAndPage_NewestFirst(t *testing.T) {
	m := eventstore.NewMemory(10)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		m.Record(eventstore.Event{
			Time:   base.Add(time.Duration(i) * time.Minute),
			Source: eventstore.SourceAudit,
			Action: "CREATE",
		})
	}
	page := m.Page(eventstore.Query{Limit: 10})
	if len(page.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(page.Rows))
	}
	// Newest (i=2) first.
	if !page.Rows[0].Time.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("rows[0].Time = %v, want the newest", page.Rows[0].Time)
	}
	if !page.Rows[2].Time.Equal(base) {
		t.Errorf("rows[2].Time = %v, want the oldest", page.Rows[2].Time)
	}
	if page.Matched != 3 || page.Retained != 3 {
		t.Errorf("matched/retained = %d/%d, want 3/3", page.Matched, page.Retained)
	}
	if page.Truncated {
		t.Error("truncated = true, want false (ring not full)")
	}
}

func TestZeroTimeStampedWithClock(t *testing.T) {
	fixed := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	m := eventstore.NewMemory(5, eventstore.WithClock(func() time.Time { return fixed }))
	m.Record(eventstore.Event{Source: eventstore.SourceWebhook})
	page := m.Page(eventstore.Query{Limit: 1})
	if len(page.Rows) != 1 || !page.Rows[0].Time.Equal(fixed) {
		t.Fatalf("rows = %+v, want one row stamped %v", page.Rows, fixed)
	}
}

// TestEviction_DrivesTheBoundary specifically drives the ring past capacity so
// the eviction path (not just the "under capacity" path) is exercised, per the
// repo's own guard-test lesson: a test that counts entries is blind to a bug
// that leaves the count unchanged.
func TestEviction_DrivesTheBoundary(t *testing.T) {
	m := eventstore.NewMemory(3)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		m.Record(eventstore.Event{Time: base.Add(time.Duration(i) * time.Minute), Source: eventstore.SourceAudit})
	}
	stats := m.Stats()
	if stats.Retained != 3 {
		t.Fatalf("retained = %d, want 3 (capacity)", stats.Retained)
	}
	if stats.Recorded != 5 {
		t.Fatalf("recorded = %d, want 5", stats.Recorded)
	}
	if stats.Evicted != 2 {
		t.Fatalf("evicted = %d, want 2 (5 recorded - 3 capacity)", stats.Evicted)
	}
	page := m.Page(eventstore.Query{Limit: 10})
	if !page.Truncated {
		t.Error("truncated = false, want true (ring at capacity)")
	}
	// The two oldest (i=0,1) must be gone; only i=2,3,4 remain.
	if !page.Rows[len(page.Rows)-1].Time.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("oldest retained = %v, want i=2 (i=0,1 evicted)", page.Rows[len(page.Rows)-1].Time)
	}
}

func TestReclaimAfterManyEvictions_RingStaysCorrect(t *testing.T) {
	// Push well past capacity*2 so the dead-prefix reclaim (m.start >= capacity)
	// fires more than once, exercising the same compaction path flowstore's
	// recent ring relies on.
	m := eventstore.NewMemory(4)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const n = 50
	for i := range n {
		m.Record(eventstore.Event{Time: base.Add(time.Duration(i) * time.Minute), Action: "x"})
	}
	stats := m.Stats()
	if stats.Retained != 4 {
		t.Fatalf("retained = %d, want 4", stats.Retained)
	}
	if stats.Recorded != n {
		t.Fatalf("recorded = %d, want %d", stats.Recorded, n)
	}
	if stats.Evicted != n-4 {
		t.Fatalf("evicted = %d, want %d", stats.Evicted, n-4)
	}
	page := m.Page(eventstore.Query{Limit: 10})
	if len(page.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(page.Rows))
	}
	if !page.Rows[0].Time.Equal(base.Add((n - 1) * time.Minute)) {
		t.Errorf("newest row = %v, want the last recorded", page.Rows[0].Time)
	}
}

func TestFilter_EachDimension(t *testing.T) {
	m := eventstore.NewMemory(10)
	m.Record(eventstore.Event{Source: eventstore.SourceAudit, Action: "CREATE", Type: "NODE",
		ActorID: "u1", ActorName: "alice@example.com", TargetID: "n1", TargetName: "camden",
		Severity: eventstore.SeverityInfo})
	m.Record(eventstore.Event{Source: eventstore.SourceWebhook, Action: "nodeDeleted", Type: "nodeDeleted",
		ActorID: "u2", ActorName: "bob@example.com", TargetID: "n2", TargetName: "mbp16",
		Severity: eventstore.SeverityWarn, Error: "boom"})

	tests := []struct {
		name   string
		filter eventstore.Filter
		want   int
	}{
		{"source audit", eventstore.Filter{Source: "audit"}, 1},
		{"source webhook case-insensitive", eventstore.Filter{Source: "WEBHOOK"}, 1},
		{"actor substring by id", eventstore.Filter{Actor: "u1"}, 1},
		{"actor substring by name", eventstore.Filter{Actor: "bob"}, 1},
		{"action substring", eventstore.Filter{Action: "delet"}, 1},
		{"target substring by name", eventstore.Filter{Target: "camden"}, 1},
		{"severity exact", eventstore.Filter{Severity: "warn"}, 1},
		{"type exact", eventstore.Filter{Type: "NODE"}, 1},
		{"errors only", eventstore.Filter{ErrorsOnly: true}, 1},
		{"no match", eventstore.Filter{Actor: "nobody"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := m.Page(eventstore.Query{Limit: 10, Filter: tt.filter})
			if page.Matched != tt.want {
				t.Errorf("matched = %d, want %d (rows=%+v)", page.Matched, tt.want, page.Rows)
			}
		})
	}
}

func TestFilter_TypeIsExactNotSubstring(t *testing.T) {
	// A closed-enumeration field must not let a substring match something it
	// shouldn't — mirrors flowstore.RecentFilter's Type/Verdict/Path rationale.
	m := eventstore.NewMemory(10)
	m.Record(eventstore.Event{Type: "nodeCreated"})
	page := m.Page(eventstore.Query{Limit: 10, Filter: eventstore.Filter{Type: "node"}})
	if page.Matched != 0 {
		t.Errorf("matched = %d, want 0: exact-match filter must not substring-match", page.Matched)
	}
}

func TestPage_CursorPagination(t *testing.T) {
	m := eventstore.NewMemory(10)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		m.Record(eventstore.Event{Time: base.Add(time.Duration(i) * time.Minute)})
	}
	first := m.Page(eventstore.Query{Limit: 2})
	if len(first.Rows) != 2 || first.NextCursor == 0 {
		t.Fatalf("first page = %+v, want 2 rows and a cursor", first)
	}
	second := m.Page(eventstore.Query{Limit: 2, Cursor: first.NextCursor})
	if len(second.Rows) != 2 {
		t.Fatalf("second page rows = %d, want 2", len(second.Rows))
	}
	// No overlap between pages.
	for _, a := range first.Rows {
		for _, b := range second.Rows {
			if a.Time.Equal(b.Time) {
				t.Errorf("page overlap: %v appears in both pages", a.Time)
			}
		}
	}
	third := m.Page(eventstore.Query{Limit: 2, Cursor: second.NextCursor})
	if len(third.Rows) != 1 {
		t.Fatalf("third page rows = %d, want 1 (5 total, 2+2 already served)", len(third.Rows))
	}
	if third.NextCursor != 0 {
		t.Errorf("third page cursor = %d, want 0 (no more pages)", third.NextCursor)
	}
}

func TestPage_WindowFiltersByTime(t *testing.T) {
	m := eventstore.NewMemory(10)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		m.Record(eventstore.Event{Time: base.Add(time.Duration(i) * time.Hour)})
	}
	page := m.Page(eventstore.Query{Start: base.Add(time.Hour), End: base.Add(3 * time.Hour), Limit: 10})
	if page.Matched != 3 {
		t.Fatalf("matched = %d, want 3 (hours 1,2,3)", page.Matched)
	}
}

func TestTruncate_LongDetailsAreCutAndMarked(t *testing.T) {
	long := strings.Repeat("a", eventstore.MaxDetailBytes+100)
	m := eventstore.NewMemory(5)
	m.Record(eventstore.Event{Details: long})
	page := m.Page(eventstore.Query{Limit: 1})
	row := page.Rows[0]
	if !row.Truncated {
		t.Error("Truncated = false, want true for an oversized Details field")
	}
	if len(row.Details) >= len(long) {
		t.Errorf("Details len = %d, want less than the original %d", len(row.Details), len(long))
	}
	if !strings.HasSuffix(row.Details, "[truncated]") {
		t.Errorf("Details = %q, want a truncation marker suffix", row.Details)
	}
}

func TestTruncate_ShortDetailsUnaffected(t *testing.T) {
	m := eventstore.NewMemory(5)
	m.Record(eventstore.Event{Details: "short"})
	page := m.Page(eventstore.Query{Limit: 1})
	if page.Rows[0].Truncated {
		t.Error("Truncated = true for a short Details field, want false")
	}
	if page.Rows[0].Details != "short" {
		t.Errorf("Details = %q, want unchanged %q", page.Rows[0].Details, "short")
	}
}
