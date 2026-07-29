package sqlitestore

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// testOpts returns a fresh Options pointed at a scratch directory, with the
// write-behind timers tightened so tests do not have to wait on the package
// defaults (5s flush, 1h sweep).
func testOpts(t *testing.T) Options {
	t.Helper()
	return Options{
		Dir:           t.TempDir(),
		Tailnet:       "test",
		Retention:     time.Hour,
		MaxFutureSkew: time.Hour,
		QueueSize:     64,
		BatchSize:     8,
		FlushInterval: 20 * time.Millisecond,
		QueryTimeout:  2 * time.Second,
		SweepInterval: time.Hour, // tests that care about sweeping call s.sweep directly
		Now:           time.Now,
	}
}

func openTestStore(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// obs builds a fully-populated Observation at time tm, so persistence tests
// have every column to round-trip.
func obs(tm time.Time) flowstore.Observation {
	return flowstore.Observation{
		Time:        tm,
		TrafficType: "overlay",
		Transport:   "tcp",
		SrcAddr:     "100.64.0.1:1234",
		DstAddr:     "100.64.0.2:443",
		SrcNode:     "node-a",
		DstNode:     "node-b",
		DstPort:     "443",
		DstService:  "https",
		SrcUser:     "alice@example.com",
		DstUser:     "bob@example.com",
		Verdict:     flowstore.VerdictPermitted,
		Rule:        2,
		Counts:      flowstore.Counts{TxBytes: 10, RxBytes: 20, TxPkts: 1, RxPkts: 2, Flows: 1},
	}
}

func rowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM flows").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// waitForRows polls until at least want rows are visible or timeout elapses.
// The write-behind path is asynchronous by design, so tests that assert on
// persisted content must wait for the drain goroutine rather than assume a
// synchronous write.
func waitForRows(t *testing.T, db *sql.DB, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if n := rowCount(t, db); n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d rows, have %d", want, rowCount(t, db))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// (a) a recorded observation is durably readable after Close and reopen.
func TestRecordResult_DurableAfterCloseReopen(t *testing.T) {
	opts := testOpts(t)
	now := time.Now().UTC()
	opts.Now = func() time.Time { return now }
	s := openTestStore(t, opts)

	o := obs(now)
	if adm := s.RecordResult(o); adm != flowstore.AdmissionAccepted {
		t.Fatalf("RecordResult admission = %v, want Accepted", adm)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	if n := rowCount(t, s2.db); n != 1 {
		t.Fatalf("row count after reopen = %d, want 1", n)
	}

	var srcUser string
	if err := s2.db.QueryRow("SELECT src_user FROM flows LIMIT 1").Scan(&srcUser); err != nil {
		t.Fatalf("query src_user: %v", err)
	}
	if srcUser != o.SrcUser {
		t.Fatalf("src_user = %q, want %q", srcUser, o.SrcUser)
	}
}

// (b) queue-full drops and counts rather than blocking. Built directly
// (bypassing Open) so no drain goroutine ever empties the queue: with a
// QueueSize of 1, the second send is deterministically full.
func TestRecordResult_QueueFullDrops(t *testing.T) {
	opts := testOpts(t)
	opts.applyDefaults()
	s := &Store{
		opts:  opts,
		queue: make(chan flowstore.Observation, 1),
		done:  make(chan struct{}),
	}

	now := opts.Now()
	if adm := s.RecordResult(obs(now)); adm != flowstore.AdmissionAccepted {
		t.Fatalf("first RecordResult admission = %v, want Accepted", adm)
	}
	if adm := s.RecordResult(obs(now)); adm != flowstore.AdmissionAccepted {
		t.Fatalf("second RecordResult admission = %v, want Accepted (admission, not enqueue, is what's asserted)", adm)
	}

	s.mu.Lock()
	recorded, drops := s.recorded, s.drops
	s.mu.Unlock()

	if recorded != 1 {
		t.Fatalf("recorded = %d, want 1", recorded)
	}
	if drops != 1 {
		t.Fatalf("drops = %d, want 1", drops)
	}
	if len(s.queue) != 1 {
		t.Fatalf("queue len = %d, want 1 (still full, second observation dropped not queued)", len(s.queue))
	}
}

// (c) expired/future observations are rejected by admission and never
// enqueued at all.
func TestRecordResult_AdmissionRejectsExpiredAndFuture(t *testing.T) {
	opts := testOpts(t)
	now := time.Now().UTC()
	opts.Now = func() time.Time { return now }
	opts.applyDefaults()
	s := &Store{
		opts:  opts,
		queue: make(chan flowstore.Observation, 8),
		done:  make(chan struct{}),
	}

	expired := obs(now.Add(-2 * opts.Retention))
	if adm := s.RecordResult(expired); adm != flowstore.AdmissionExpired {
		t.Fatalf("expired admission = %v, want AdmissionExpired", adm)
	}

	future := obs(now.Add(2 * opts.MaxFutureSkew))
	if adm := s.RecordResult(future); adm != flowstore.AdmissionFuture {
		t.Fatalf("future admission = %v, want AdmissionFuture", adm)
	}

	if len(s.queue) != 0 {
		t.Fatalf("queue len = %d, want 0 (rejected observations must never enqueue)", len(s.queue))
	}
}

// (d) Redact is applied before persistence: the field it clears must not
// reach disk, even though the in-memory Observation the caller passed in
// still carries it.
func TestRecordResult_RedactAppliedBeforePersist(t *testing.T) {
	opts := testOpts(t)
	now := time.Now().UTC()
	opts.Now = func() time.Time { return now }
	opts.Redact = func(o *flowstore.Observation) { o.SrcUser = "" }
	s := openTestStore(t, opts)

	o := obs(now)
	if o.SrcUser == "" {
		t.Fatal("test fixture must carry a non-empty SrcUser for this test to mean anything")
	}
	if adm := s.RecordResult(o); adm != flowstore.AdmissionAccepted {
		t.Fatalf("admission = %v, want Accepted", adm)
	}

	waitForRows(t, s.db, 1, time.Second)

	var srcUser string
	if err := s.db.QueryRow("SELECT src_user FROM flows LIMIT 1").Scan(&srcUser); err != nil {
		t.Fatalf("query src_user: %v", err)
	}
	if srcUser != "" {
		t.Fatalf("src_user = %q, want empty (Redact must run before the row is written)", srcUser)
	}
}

// (e) the retention sweep deletes rows once the clock moves past Retention.
func TestSweep_DeletesExpiredRows(t *testing.T) {
	opts := testOpts(t)
	var mu sync.Mutex
	current := time.Now().UTC()
	opts.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	s := openTestStore(t, opts)

	if adm := s.RecordResult(obs(current)); adm != flowstore.AdmissionAccepted {
		t.Fatalf("admission = %v, want Accepted", adm)
	}
	waitForRows(t, s.db, 1, time.Second)

	mu.Lock()
	current = current.Add(2 * opts.Retention)
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if n := rowCount(t, s.db); n != 0 {
		t.Fatalf("row count after sweep = %d, want 0 (row is well past retention)", n)
	}
}

// (f) MaxRows enforcement deletes the OLDEST excess rows, keeping the newest
// MaxRows.
func TestSweep_MaxRowsCapDeletesOldest(t *testing.T) {
	opts := testOpts(t)
	base := time.Now().UTC()
	// Fixed "now" an hour after every observation below, so nothing looks
	// expired under Retention and only the MaxRows cap is exercised.
	opts.Now = func() time.Time { return base.Add(time.Hour) }
	opts.Retention = 24 * time.Hour
	opts.MaxRows = 3
	s := openTestStore(t, opts)

	times := make([]time.Time, 5)
	for i := range times {
		times[i] = base.Add(time.Duration(i) * time.Second)
		if adm := s.RecordResult(obs(times[i])); adm != flowstore.AdmissionAccepted {
			t.Fatalf("observation %d admission = %v, want Accepted", i, adm)
		}
	}
	waitForRows(t, s.db, 5, time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if n := rowCount(t, s.db); n != 3 {
		t.Fatalf("row count after sweep = %d, want 3 (MaxRows cap)", n)
	}

	var minTime int64
	if err := s.db.QueryRow("SELECT MIN(time) FROM flows").Scan(&minTime); err != nil {
		t.Fatalf("query min time: %v", err)
	}
	// The two oldest rows (index 0 and 1) must be the ones evicted, so the
	// earliest surviving row is index 2's timestamp.
	if want := timeToDB(times[2]); minTime != want {
		t.Fatalf("earliest surviving time = %d, want %d (oldest two rows should have been evicted)", minTime, want)
	}
}

// (g) Close drains whatever is still buffered before it returns. FlushInterval
// and BatchSize are both set so large that only the shutdown drain — not the
// periodic ticker or a full batch — could be what persists these rows.
func TestClose_DrainsPendingRows(t *testing.T) {
	opts := testOpts(t)
	opts.FlushInterval = time.Hour
	opts.BatchSize = 100
	now := time.Now().UTC()
	opts.Now = func() time.Time { return now }
	s := openTestStore(t, opts)

	const n = 3
	for i := 0; i < n; i++ {
		o := obs(now.Add(time.Duration(i) * time.Millisecond))
		if adm := s.RecordResult(o); adm != flowstore.AdmissionAccepted {
			t.Fatalf("observation %d admission = %v, want Accepted", i, adm)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// s.db is closed along with the store; reopen against the same file to
	// verify what actually reached disk.
	s2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	if got := rowCount(t, s2.db); got != n {
		t.Fatalf("row count after Close+reopen = %d, want %d (Close must drain the queue)", got, n)
	}
}

// Stats must report accurate counters and never panic, degrading gracefully
// on a read error rather than propagating one to the caller.
func TestStats_ReportsCounts(t *testing.T) {
	opts := testOpts(t)
	now := time.Now().UTC()
	opts.Now = func() time.Time { return now }
	s := openTestStore(t, opts)

	if adm := s.RecordResult(obs(now)); adm != flowstore.AdmissionAccepted {
		t.Fatalf("admission = %v, want Accepted", adm)
	}
	waitForRows(t, s.db, 1, time.Second)

	st := s.Stats()
	if st.Observations != 1 {
		t.Fatalf("Observations = %d, want 1", st.Observations)
	}
	if st.Truncated != 0 {
		t.Fatalf("Truncated = %d, want 0", st.Truncated)
	}
	if !st.Backend.Healthy {
		t.Fatalf("Backend.Healthy = false, want true (err=%q)", st.Backend.Error)
	}
	if st.Backend.Kind != flowstore.BackendSQLite {
		t.Fatalf("Backend.Kind = %q, want %q", st.Backend.Kind, flowstore.BackendSQLite)
	}
	if st.Backend.Rows != 1 {
		t.Fatalf("Backend.Rows = %d, want 1", st.Backend.Rows)
	}
	if st.Earliest.IsZero() || st.Latest.IsZero() {
		t.Fatal("Earliest/Latest should be set once a row exists")
	}
}
