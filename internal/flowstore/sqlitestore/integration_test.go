package sqlitestore

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

// The unit tests in this package each exercise one side of the store in
// isolation: writer_test reads its rows back with raw SQL, while query_test and
// recent_test seed their fixtures with raw SQL. That leaves the actual seam —
// an Observation going in through Record and coming back out through Query and
// RecentPage — covered by nothing, which is precisely the join the admin flow
// view depends on. These tests drive only the public surface.

// waitForPersistedRows blocks until the write-behind queue has drained n rows to disk, so
// a test asserting on a read is not racing the writer goroutine. It polls rather
// than sleeping a fixed interval because the flush is timer-driven.
func waitForPersistedRows(t *testing.T, s *Store, n int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.Stats().Backend.Rows >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d rows; Stats() = %+v", n, s.Stats())
}

func openIntegrationStore(t *testing.T, mutate func(*Options)) *Store {
	t.Helper()
	opts := Options{
		Dir:     t.TempDir(),
		Tailnet: "acme.example.com",
		// Flush promptly so the test is not waiting on the 5s default.
		FlushInterval: 20 * time.Millisecond,
		BatchSize:     8,
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

func integrationObs(at time.Time, src, dst string, bytes int64) flowstore.Observation {
	return flowstore.Observation{
		Time:        at,
		TrafficType: "virtual",
		Transport:   "tcp",
		SrcAddr:     "100.64.0.1",
		DstAddr:     "100.64.0.2",
		SrcNode:     src,
		DstNode:     dst,
		DstPort:     "443",
		DstService:  "https",
		SrcUser:     "someone@example.com",
		Verdict:     flowstore.VerdictPermitted,
		Rule:        3,
		Counts:      flowstore.Counts{TxBytes: bytes, RxBytes: 1, Flows: 1},
	}
}

// Record -> Query is the path the /flows topology and breakdowns take.
func TestIntegrationRecordThenQuery(t *testing.T) {
	s := openIntegrationStore(t, nil)
	now := time.Now().UTC().Add(-time.Minute)

	for i := range 5 {
		s.Record(integrationObs(now.Add(time.Duration(i)*time.Second), "laptop", "server", 100))
	}
	waitForPersistedRows(t, s, 5)

	res := s.Query(flowstore.Query{Start: now.Add(-time.Hour), End: time.Now().UTC(), TopN: 10})

	if want := int64(500); res.Totals.TxBytes != want {
		t.Fatalf("Totals.TxBytes = %d, want %d", res.Totals.TxBytes, want)
	}
	if len(res.Pairs) != 1 {
		t.Fatalf("len(Pairs) = %d, want 1: %+v", len(res.Pairs), res.Pairs)
	}
	if got := res.Pairs[0].PairKey; got.Src != "laptop" || got.Dst != "server" {
		t.Fatalf("Pairs[0] key = %+v, want laptop->server", got)
	}
	// Aggregates are exact on this backend; nothing folded into __other__.
	if res.Truncated != 0 {
		t.Fatalf("Truncated = %d, want 0 (this backend does not cap-fold)", res.Truncated)
	}
	if len(res.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2 (sender and receiver)", len(res.Nodes))
	}
}

// Record -> RecentPage is the path the raw-connection list and the exports take.
func TestIntegrationRecordThenRecentPage(t *testing.T) {
	s := openIntegrationStore(t, nil)
	now := time.Now().UTC().Add(-time.Minute)

	for i := range 5 {
		s.Record(integrationObs(now.Add(time.Duration(i)*time.Second), "laptop", "server", int64(i+1)))
	}
	waitForPersistedRows(t, s, 5)

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10})
	if len(page.Rows) != 5 {
		t.Fatalf("len(Rows) = %d, want 5", len(page.Rows))
	}
	if page.Matched != 5 || page.Retained != 5 {
		t.Fatalf("Matched/Retained = %d/%d, want 5/5", page.Matched, page.Retained)
	}
	// Newest first: the last observation recorded carries the largest TxBytes.
	if page.Rows[0].Counts.TxBytes != 5 {
		t.Fatalf("Rows[0].TxBytes = %d, want 5 (newest first)", page.Rows[0].Counts.TxBytes)
	}
	if page.Rows[0].SrcNode != "laptop" || page.Rows[0].DstService != "https" {
		t.Fatalf("Rows[0] lost fields on the round trip: %+v", page.Rows[0])
	}
}

// The whole point of the feature: what was recorded before a restart is still
// there after one. This reopens the SAME directory with a new Store.
func TestIntegrationSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	opts := Options{Dir: dir, Tailnet: "acme.example.com", FlushInterval: 20 * time.Millisecond}

	first, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	for i := range 3 {
		first.Record(integrationObs(now.Add(time.Duration(i)*time.Second), "laptop", "server", 10))
	}
	// Close drains what is queued, so the rows must be durable afterwards without
	// any wait — that is the contract a clean shutdown owes the operator.
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	if got := second.Stats().Backend.Rows; got != 3 {
		t.Fatalf("rows after restart = %d, want 3", got)
	}
	res := second.Query(flowstore.Query{Start: now.Add(-time.Hour), End: time.Now().UTC(), TopN: 10})
	if want := int64(30); res.Totals.TxBytes != want {
		t.Fatalf("Totals.TxBytes after restart = %d, want %d", res.Totals.TxBytes, want)
	}
}

// A second Open on the same directory must land on the same file, and a
// different tailnet must not.
func TestIntegrationPerTailnetIsolation(t *testing.T) {
	dir := t.TempDir()
	a := openIntegrationStore(t, func(o *Options) { o.Dir = dir; o.Tailnet = "acme.example.com" })
	b := openIntegrationStore(t, func(o *Options) { o.Dir = dir; o.Tailnet = "beta.example.com" })

	a.Record(integrationObs(time.Now().UTC().Add(-time.Minute), "laptop", "server", 7))
	waitForPersistedRows(t, a, 1)

	if got := b.Stats().Backend.Rows; got != 0 {
		t.Fatalf("beta saw %d rows from acme; the stores are not isolated", got)
	}
}

// Redact runs on the write path, so a disabled category must not reach disk —
// asserted through the public read surface rather than by inspecting the file.
func TestIntegrationRedactAppliesBeforeDisk(t *testing.T) {
	s := openIntegrationStore(t, func(o *Options) {
		o.Redact = func(obs *flowstore.Observation) { obs.SrcUser = "" }
	})
	s.Record(integrationObs(time.Now().UTC().Add(-time.Minute), "laptop", "server", 1))
	waitForPersistedRows(t, s, 1)

	page := s.RecentPage(flowstore.RecentQuery{Limit: 10})
	if len(page.Rows) != 1 {
		t.Fatalf("len(Rows) = %d, want 1", len(page.Rows))
	}
	if page.Rows[0].SrcUser != "" {
		t.Fatalf("SrcUser = %q, want it redacted before persistence", page.Rows[0].SrcUser)
	}
	// The rest of the row must survive: a redacted row is incomplete, not broken.
	if page.Rows[0].SrcNode != "laptop" {
		t.Fatalf("SrcNode = %q, want it untouched", page.Rows[0].SrcNode)
	}
}
