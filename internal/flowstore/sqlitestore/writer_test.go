package sqlitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
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

func TestOpen_EnablesIncrementalAutoVacuum(t *testing.T) {
	s := openTestStore(t, testOpts(t))

	var mode int
	if err := s.db.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	if mode != 2 { // SQLITE_AUTO_VACUUM_INCREMENTAL
		t.Fatalf("auto_vacuum mode = %d, want incremental (2)", mode)
	}
}

type conversionQueryContextKey struct{}

type conversionContextProbe struct {
	queryCount int
	vacuumCtx  context.Context
}

type conversionContextConnector struct {
	probe *conversionContextProbe
}

func (c *conversionContextConnector) Connect(context.Context) (driver.Conn, error) {
	return &conversionContextConn{probe: c.probe}, nil
}

func (c *conversionContextConnector) Driver() driver.Driver { return c }

func (c *conversionContextConnector) Open(string) (driver.Conn, error) {
	return c.Connect(context.Background())
}

type conversionContextConn struct {
	probe *conversionContextProbe
}

func (c *conversionContextConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("conversion context probe does not prepare statements")
}

func (c *conversionContextConn) Close() error { return nil }

func (c *conversionContextConn) Begin() (driver.Tx, error) {
	return nil, errors.New("conversion context probe does not begin transactions")
}

func (c *conversionContextConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if query != "PRAGMA auto_vacuum" {
		return nil, fmt.Errorf("conversion context probe query = %q, want PRAGMA auto_vacuum", query)
	}
	c.probe.queryCount++
	mode := int64(0)
	if c.probe.queryCount == 3 {
		mode = 2
	}
	return &singleIntRows{value: mode}, nil
}

func (c *conversionContextConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	switch query {
	case "PRAGMA auto_vacuum = 2":
		return driver.RowsAffected(0), nil
	case "VACUUM":
		c.probe.vacuumCtx = ctx
		return driver.RowsAffected(0), nil
	default:
		return nil, fmt.Errorf("conversion context probe exec = %q, want auto-vacuum setup or VACUUM", query)
	}
}

type singleIntRows struct {
	value int64
	read  bool
}

func (r *singleIntRows) Columns() []string { return []string{"auto_vacuum"} }
func (r *singleIntRows) Close() error      { return nil }

func (r *singleIntRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0] = r.value
	return nil
}

func TestOpen_ConvertsExistingDatabaseOutsideQueryTimeout(t *testing.T) {
	probe := &conversionContextProbe{}
	db := sql.OpenDB(&conversionContextConnector{probe: probe})
	t.Cleanup(func() { _ = db.Close() })

	// The marker represents Open's short-lived query context. The one-hour
	// conversion budget is never waited out: the fake VACUUM returns
	// synchronously and the test only verifies that it receives its own bounded
	// context rather than inheriting the query context.
	queryCtx := context.WithValue(context.Background(), conversionQueryContextKey{}, "query timeout")
	if err := configureIncrementalAutoVacuum(queryCtx, db, time.Hour); err != nil {
		t.Fatalf("configure incremental auto-vacuum: %v", err)
	}
	if probe.vacuumCtx == nil {
		t.Fatal("VACUUM was not called for the legacy auto-vacuum mode")
	}
	if got := probe.vacuumCtx.Value(conversionQueryContextKey{}); got != nil {
		t.Fatalf("VACUUM inherited the query context marker %q", got)
	}
	if _, ok := probe.vacuumCtx.Deadline(); !ok {
		t.Fatal("VACUUM context has no conversion deadline")
	}
	if probe.queryCount != 3 {
		t.Fatalf("auto-vacuum mode queries = %d, want 3", probe.queryCount)
	}
}

// A full VACUUM is a one-time optimization, not a condition for reading the
// existing store. In particular, an upgrade must not disable the flow explorer
// when the rewrite cannot finish within its budget: the old auto-vacuum mode
// remains a valid SQLite database and is retried on a later restart.
func TestOpen_ConversionTimeoutFailsOpenAndPreservesExistingDatabase(t *testing.T) {
	opts := testOpts(t)
	opts.ConversionTimeout = time.Nanosecond

	path := filepath.Join(opts.Dir, dbFileName(opts.Tailnet))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec("PRAGMA auto_vacuum = NONE"); err != nil {
		t.Fatalf("disable auto-vacuum: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE legacy_payload (data BLOB NOT NULL)"); err != nil {
		t.Fatalf("create legacy payload: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create legacy metadata: %v", err)
	}
	if _, err := db.Exec("INSERT INTO metadata(key, value) VALUES ('tailnet', ?)", opts.Tailnet); err != nil {
		t.Fatalf("stamp legacy tailnet identity: %v", err)
	}
	// 32 MiB is deliberately large enough to force the tested path to be a
	// real database rewrite, not an empty-file shortcut.
	if _, err := db.Exec("INSERT INTO legacy_payload(data) VALUES (zeroblob(?))", 32*1024*1024); err != nil {
		t.Fatalf("populate legacy database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat realistic legacy database: %v", err)
	}
	if info.Size() < 32*1024*1024 {
		t.Fatalf("legacy database size = %d, want at least 32 MiB", info.Size())
	}

	s, err := Open(opts)
	if err != nil {
		t.Fatalf("Open must retain a usable legacy store when conversion times out: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var mode int
	if err := s.db.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatalf("read retained auto-vacuum mode: %v", err)
	}
	if mode != 0 { // SQLITE_AUTO_VACUUM_NONE
		t.Fatalf("auto_vacuum mode = %d, want none (0) until a future conversion succeeds", mode)
	}

	var payloadBytes int
	if err := s.db.QueryRow("SELECT length(data) FROM legacy_payload").Scan(&payloadBytes); err != nil {
		t.Fatalf("read legacy payload after failed conversion: %v", err)
	}
	if payloadBytes != 32*1024*1024 {
		t.Fatalf("legacy payload bytes = %d, want %d", payloadBytes, 32*1024*1024)
	}

	if err := s.db.QueryRow("SELECT COUNT(*) FROM flows").Scan(&payloadBytes); err != nil {
		t.Fatalf("query migrated flow table: %v", err)
	}
	if backend := s.Stats().Backend; backend.Healthy {
		t.Fatal("Backend.Healthy = true, want false while auto-vacuum conversion is deferred")
	} else if !strings.Contains(backend.Error, "convert database to incremental auto-vacuum") {
		t.Fatalf("Backend.Error = %q, want the deferred conversion failure", backend.Error)
	}

	if admission := s.RecordResult(obs(time.Now().UTC())); admission != flowstore.AdmissionAccepted {
		t.Fatalf("RecordResult admission = %v, want accepted", admission)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close retained store: %v", err)
	}
	reopened, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen retained store after deferred conversion: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	waitForRows(t, reopened.db, 1, time.Second)
}

func TestDeferredAutoVacuumConversionGuard(t *testing.T) {
	if !isDeferredAutoVacuumConversion(deferredAutoVacuumConversionError{err: errors.New("vacuum timed out")}) {
		t.Fatal("deferred VACUUM conversion was not recognized")
	}
	if isDeferredAutoVacuumConversion(errors.New("open database failed")) {
		t.Fatal("unrelated database setup error was incorrectly treated as a deferred conversion")
	}
}

func TestOptions_VacuumCadenceDefaultsToSweepAndOverrides(t *testing.T) {
	opts := testOpts(t)
	opts.SweepInterval = 7 * time.Minute
	opts.IncrementalVacuumInterval = 0
	opts.applyDefaults()
	if got := opts.vacuumInterval(); got != opts.SweepInterval {
		t.Fatalf("vacuumInterval() = %v, want sweep interval %v", got, opts.SweepInterval)
	}

	opts.IncrementalVacuumInterval = 11 * time.Minute
	if got := opts.vacuumInterval(); got != opts.IncrementalVacuumInterval {
		t.Fatalf("vacuumInterval() = %v, want explicit interval %v", got, opts.IncrementalVacuumInterval)
	}
}

func TestStats_ReportsJournalAndCheckpoint(t *testing.T) {
	opts := testOpts(t)
	s := openTestStore(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.checkpoint(ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	st := s.Stats()
	if st.Backend.LastCheckpointAt.IsZero() {
		t.Fatal("LastCheckpointAt is zero after a successful checkpoint")
	}
	walPath := s.path + "-wal"
	walInfo, err := os.Stat(walPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat WAL %s: %v", walPath, err)
	}
	var want int64
	if err == nil {
		want = walInfo.Size()
	}
	if st.Backend.JournalSizeBytes != want {
		t.Fatalf("JournalSizeBytes = %d, want current WAL size %d", st.Backend.JournalSizeBytes, want)
	}
}

func TestCheckpointCompletedRequiresEveryWALFrame(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		busy, logFrames, checkpointed int
		want                          bool
	}{
		{name: "complete", logFrames: 8, checkpointed: 8, want: true},
		{name: "empty complete", want: true},
		{name: "busy", busy: 1, logFrames: 8, checkpointed: 8},
		{name: "partial", logFrames: 8, checkpointed: 5},
		{name: "unknown counts", logFrames: -1, checkpointed: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkpointCompleted(tc.busy, tc.logFrames, tc.checkpointed); got != tc.want {
				t.Fatalf("checkpointCompleted(%d, %d, %d) = %t, want %t", tc.busy, tc.logFrames, tc.checkpointed, got, tc.want)
			}
		})
	}
}

func TestSweepAutomaticallyReclaimsExpiredRows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOpts(t)
		// Five hundred rows with a 3,900-byte tag payload span enough pages to
		// prove the database file shrinks while remaining within the default
		// 1,000-page incremental-vacuum budget. BatchSize and QueueSize match
		// that fixture count so one full batch makes every row durable without
		// waiting for the deliberately out-of-band flush timer.
		const rowCountWant = 500
		opts.BatchSize = rowCountWant
		opts.QueueSize = rowCountWant
		opts.FlushInterval = 5 * time.Hour
		// The first fake sweep tick must occur after retention has elapsed, so
		// use a two-hour cadence for the one-hour retention window.
		opts.SweepInterval = 2 * time.Hour
		opts.IncrementalVacuumInterval = 0 // inherit the sweep cadence
		opts.Now = time.Now

		s, err := Open(opts)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = s.Close() }()

		now := time.Now()
		for i := 0; i < rowCountWant; i++ {
			o := obs(now)
			o.SrcTags = strings.Repeat("tag", 1300)
			if adm := s.RecordResult(o); adm != flowstore.AdmissionAccepted {
				t.Fatalf("observation %d admission = %v, want Accepted", i, adm)
			}
		}
		synctest.Wait()
		if got := rowCount(t, s.db); got != rowCountWant {
			t.Fatalf("rows before fake sweep tick = %d, want %d", got, rowCountWant)
		}
		before := fileSize(t, s.path)
		if before <= 0 {
			t.Fatalf("database size before reclamation = %d, want positive", before)
		}

		// The first fake sweep deletes rows and vacuums into the WAL; the second
		// checkpoints that reclamation into the database file. Both ticks are
		// after Retention, and the advance is entirely synthetic.
		time.Sleep(2 * opts.SweepInterval)
		synctest.Wait()
		if got := rowCount(t, s.db); got != 0 {
			t.Fatalf("rows after automatic sweep = %d, want 0", got)
		}
		if after := fileSize(t, s.path); after >= before {
			t.Fatalf("database size after reclamation = %d, want less than before %d", after, before)
		}
	})
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database %s: %v", path, err)
	}
	return info.Size()
}
