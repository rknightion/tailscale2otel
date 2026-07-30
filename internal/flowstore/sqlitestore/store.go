// Package sqlitestore is the opt-in persistent backend for the admin flow view
// (#294), so /flows can answer over days rather than the in-memory ring's hours
// and survive a restart.
//
// It is opt-in and off by default. The stateless single-static-binary default
// is the product promise; this exists for operators who explicitly ask for
// history by setting flows.store.directory.
//
// # Why raw rows and not the memory store's buckets
//
// internal/flowstore aggregates on the way in, at one-minute resolution, with a
// per-bucket cap on every dimension, because RAM is scarce and an
// unauthenticated ingress must not be able to grow the process without limit.
// Overflow folds into flowstore.Other and the page says coverage is partial.
//
// Disk inverts that trade. This backend stores one row per connection and
// derives every aggregate with GROUP BY at query time. Mirroring the fifteen
// per-bucket maps instead would mean up to 43,200 buckets times ten thousand
// keys at a thirty-day retention, and would inherit cap-folding that only
// exists to save memory. Three consequences follow, and they are improvements:
//
//   - Aggregates are EXACT. Result.Truncated is zero unless the write-behind
//     queue actually dropped, so "partial coverage" now means something.
//   - RecentPage pages the whole retained window, not a 2000-row ring.
//   - Bounding is by retention sweep and a hard row cap, not by key caps.
//
// # The hot-path contract still binds
//
// flowstore's package doc requires that Record never blocks, never fails and
// never propagates an error. Disk cannot honor that synchronously, so Record
// enqueues onto a bounded channel and returns; a single background goroutine
// drains it in batches inside one transaction. A full queue drops and counts,
// exactly as an overflowing bucket folds into Other. The emit path never waits
// on a disk write, and never sees an error from one.
package sqlitestore

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"

	// Registers the pure-Go "sqlite" driver. modernc.org/sqlite is a transpiled
	// SQLite, not a cgo binding, which is why it is the only engine this project
	// can use: the release matrix builds every target with CGO_ENABLED=0 and
	// ships a distroless image, and a cgo driver would end both.
	_ "modernc.org/sqlite"
)

// Defaults for the write-behind path and the retention bound. Every one is
// overridable through flows.store.*; these are the values an operator gets by
// setting nothing but the path.
const (
	DefaultRetention     = 30 * 24 * time.Hour
	DefaultMaxRows       = 5_000_000
	DefaultMaxExportRows = 50_000
	DefaultQueueSize     = 8192
	DefaultBatchSize     = 512
	DefaultFlushInterval = 5 * time.Second
	DefaultQueryTimeout  = 15 * time.Second
	DefaultSweepInterval = time.Hour
)

// Options configures one per-tailnet database.
//
// A device name is unique only within its tailnet, so each tailnet gets its own
// store and its own file — a shared one would merge two different machines into
// one vertex of the topology, and would put every tailnet behind a single
// writer lock.
type Options struct {
	// Dir is the configured flows.store.directory: a DIRECTORY, not a file. The
	// filename is derived from Tailnet so multi-tailnet mode needs no extra
	// configuration and cannot collide.
	Dir string
	// Tailnet names the tailnet this store serves.
	Tailnet string
	// Retention is how far back the store keeps rows. Rows older than this are
	// swept. Distinct from flows.retention, which still sizes the memory ring.
	Retention time.Duration
	// MaxRows is a hard cap on retained rows, enforced independently of
	// Retention so a traffic flood cannot fill the disk before the sweep runs.
	MaxRows int64
	// MaxExportRows bounds what one CSV/JSON export may read. Reported as
	// Limits().MaxRecent, which is what maxExportRows reads. Without it an
	// export would try to materialize the whole retained set.
	MaxExportRows int
	// MaxFutureSkew is the largest amount a record may lead the local clock and
	// still be admitted. Same meaning as the memory store's.
	MaxFutureSkew time.Duration
	// QueueSize bounds the write-behind channel. Full means drop-and-count.
	QueueSize int
	// BatchSize is how many rows one transaction writes.
	BatchSize int
	// FlushInterval forces a partial batch to disk so a quiet tailnet's last few
	// connections do not sit in memory indefinitely.
	FlushInterval time.Duration
	// QueryTimeout bounds a single read. A window scan that exceeds it fails
	// honestly rather than hanging the admin page.
	QueryTimeout time.Duration
	// SweepInterval is how often retention and the row cap are enforced.
	SweepInterval time.Duration
	// Now is the clock, injectable for tests.
	Now func() time.Time
	// Redact applies the configured PII policy to an observation BEFORE it is
	// persisted. This matters more here than anywhere else in the package: in
	// memory an email address dies with the process, but on disk it persists and
	// gets backed up. If the filter would strip an identity from telemetry, it
	// must not reach the file either. Nil means no redaction.
	Redact func(*flowstore.Observation)
}

func (o *Options) applyDefaults() {
	if o.Retention <= 0 {
		o.Retention = DefaultRetention
	}
	if o.MaxRows <= 0 {
		o.MaxRows = DefaultMaxRows
	}
	if o.MaxExportRows <= 0 {
		o.MaxExportRows = DefaultMaxExportRows
	}
	if o.QueueSize <= 0 {
		o.QueueSize = DefaultQueueSize
	}
	if o.BatchSize <= 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = DefaultFlushInterval
	}
	if o.QueryTimeout <= 0 {
		o.QueryTimeout = DefaultQueryTimeout
	}
	if o.SweepInterval <= 0 {
		o.SweepInterval = DefaultSweepInterval
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Store is the persistent flowstore.Store implementation. It satisfies the same
// seam as flowstore.Memory, so the admin handlers do not know which they hold.
type Store struct {
	opts Options
	db   *sql.DB
	path string

	// queue carries observations from the emit path to the writer goroutine.
	// Buffered and bounded: a send that would block is dropped instead.
	queue chan flowstore.Observation
	done  chan struct{}
	wg    sync.WaitGroup

	mu       sync.Mutex
	recorded int64
	drops    int64
	failure  error
}

var _ flowstore.Store = (*Store)(nil)

// fail records a backend failure. The view stays up and keeps serving what it
// has — a read error must not take the admin page down — but Stats reports
// unhealthy so the operator is told the data is no longer trustworthy rather
// than silently reading a stale or partial window.
func (s *Store) fail(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = err
}

// Limits reports the effective bounds. The per-bucket cap fields the memory
// store fills are zero here and that is honest: this backend does not fold keys
// into flowstore.Other, so there are no per-dimension caps to report. MaxRecent
// carries MaxExportRows because that is the only one of these a caller reads.
func (s *Store) Limits() flowstore.Limits {
	return flowstore.Limits{
		Profile:   flowstore.BackendSQLite,
		MaxRecent: s.opts.MaxExportRows,
	}
}

// Record implements flowstore.Store. It never blocks and never fails.
func (s *Store) Record(o flowstore.Observation) { _ = s.RecordResult(o) }

// admit applies the same time-bound admission the memory store applies, before
// anything touches the queue. The lower bound is the retention window rather
// than a bucket count, since that is what this backend actually keeps.
func (s *Store) admit(o *flowstore.Observation) flowstore.Admission {
	now := s.opts.Now()
	if o.Time.IsZero() {
		o.Time = now
		return flowstore.AdmissionAccepted
	}
	if o.Time.Before(now.Add(-s.opts.Retention)) {
		return flowstore.AdmissionExpired
	}
	if o.Time.After(now.Add(s.opts.MaxFutureSkew)) {
		return flowstore.AdmissionFuture
	}
	return flowstore.AdmissionAccepted
}

// String makes failures legible in logs.
func (s *Store) String() string {
	return fmt.Sprintf("sqlitestore(%s, retention=%s)", s.path, s.opts.Retention)
}
