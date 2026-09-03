package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

// RecordResult implements flowstore.Store. It honors the package's hard
// contract — never blocks, never does I/O, never returns an error to the
// caller beyond the Admission verdict — by admitting synchronously and then
// handing the (possibly redacted) observation to the write-behind queue with
// a non-blocking send. A full queue drops and counts, exactly as an
// overflowing bucket in the memory store folds into Other.
func (s *Store) RecordResult(o flowstore.Observation) flowstore.Admission {
	adm := s.admit(&o)
	if adm != flowstore.AdmissionAccepted {
		return adm
	}

	// PII must never reach disk if the configured policy would strip it from
	// telemetry — see Options.Redact's doc comment. This runs before the
	// observation is even offered to the queue.
	if s.opts.Redact != nil {
		s.opts.Redact(&o)
	}
	o = flowstore.NormalizeObservationForRetention(o)

	// Once Close has been called, the drain goroutine may already have made
	// its final pass, so an item sent after that would sit in the queue
	// forever rather than reaching disk. Treat it as a drop rather than a
	// silent leak.
	select {
	case <-s.done:
		s.mu.Lock()
		s.drops++
		s.mu.Unlock()
		return adm
	default:
	}

	select {
	case s.queue <- o:
		s.mu.Lock()
		s.recorded++
		s.mu.Unlock()
	default:
		s.mu.Lock()
		s.drops++
		s.mu.Unlock()
	}
	return adm
}

// run is the single write-behind goroutine: it drains s.queue in batches,
// flushing on a full batch, a periodic tick, or shutdown, and runs the
// retention sweep on its own slower tick. There is exactly one of these per
// Store, which is what lets flush use one unsynchronized *sql.Tx per batch
// without a second layer of locking.
func (s *Store) run() {
	defer s.wg.Done()

	flushTicker := time.NewTicker(s.opts.FlushInterval)
	defer flushTicker.Stop()
	sweepTicker := time.NewTicker(s.opts.SweepInterval)
	defer sweepTicker.Stop()
	// A zero incremental-vacuum interval is deliberately not a disabled mode:
	// it inherits the sweep cadence. Use the sweep case for that mode so one
	// tick performs delete, checkpoint and reclamation in that order. An
	// explicit interval gets its own ticker and still checkpoints after each
	// sweep, keeping journal growth observable even between vacuum ticks.
	var vacuumTicker *time.Ticker
	var vacuumC <-chan time.Time
	if s.opts.IncrementalVacuumInterval > 0 {
		vacuumTicker = time.NewTicker(s.opts.vacuumInterval())
		vacuumC = vacuumTicker.C
		defer vacuumTicker.Stop()
	}

	batch := make([]flowstore.Observation, 0, s.opts.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		_ = s.flush(batch) // errors are recorded via s.fail; nothing more to do here
		batch = batch[:0]
	}

	for {
		select {
		case o := <-s.queue:
			batch = append(batch, o)
			if len(batch) >= s.opts.BatchSize {
				flush()
			}
		case <-flushTicker.C:
			flush()
		case <-sweepTicker.C:
			ctx, cancel := context.WithTimeout(context.Background(), s.opts.QueryTimeout)
			s.maintain(ctx, true, s.opts.IncrementalVacuumInterval <= 0)
			cancel()
		case <-vacuumC:
			ctx, cancel := context.WithTimeout(context.Background(), s.opts.QueryTimeout)
			s.maintain(ctx, false, true)
			cancel()
		case <-s.done:
			// Clean shutdown: whatever is already sitting in the queue was
			// accepted as far as the emit path is concerned, so drain it
			// (non-blocking — nothing is sending anymore once RecordResult
			// observes s.done closed) and flush before exiting.
			for {
				select {
				case o := <-s.queue:
					batch = append(batch, o)
				default:
					flush()
					return
				}
			}
		}
	}
}

// flush writes one batch in a single transaction. The column list and
// placeholders are built from the shared columns slice in schema.go so the
// statement can never drift from the schema; the bind values below are in
// that same fixed order.
func (s *Store) flush(batch []flowstore.Observation) error {
	if len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.opts.QueryTimeout)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.fail(err)
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}
	// The only interpolated values are this package's own `columns` slice and a
	// matching run of "?" placeholders — both compile-time constants of the
	// schema, neither reachable from a request. Every observation value is bound
	// as a parameter below, never formatted into the statement.
	insert := fmt.Sprintf( //nolint:gosec // G201: column list is a package constant, values are bound parameters
		"INSERT INTO flows (%s) VALUES (%s)",
		strings.Join(columns, ", "), strings.Join(placeholders, ", "),
	)

	stmt, err := tx.PrepareContext(ctx, insert)
	if err != nil {
		s.fail(err)
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, o := range batch {
		if _, err := stmt.ExecContext(ctx,
			timeToDB(o.Time),
			o.TrafficType,
			o.Transport,
			o.SrcAddr,
			o.DstAddr,
			o.SrcNode,
			o.DstNode,
			o.DstPort,
			o.DstService,
			o.SrcUser,
			o.DstUser,
			o.SrcTags,
			o.DstTags,
			o.SrcOS,
			o.DstOS,
			o.ReporterNodeID,
			o.ReporterTrust,
			o.ReporterConsistency,
			o.Verdict,
			boolToDB(o.Reversed),
			o.Rule,
			o.PolicyVersion,
			o.Path,
			o.DERPRegion,
			o.Counts.TxBytes,
			o.Counts.RxBytes,
			o.Counts.TxPkts,
			o.Counts.RxPkts,
			o.Counts.Flows,
		); err != nil {
			s.fail(err)
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		s.fail(err)
		return err
	}
	// Checkpoint after a committed batch so the WAL sidecar does not grow
	// without bound during a quiet period, and so Stats can report a real
	// last-successful-checkpoint time even when the sweep cadence is long.
	checkpointCtx, checkpointCancel := s.queryCtx()
	_ = s.checkpoint(checkpointCtx)
	checkpointCancel()
	return nil
}

// maintain runs one bounded writer-maintenance pass. The sweep is optional
// because an explicit vacuum interval may fall between retention sweeps. A
// checkpoint precedes incremental_vacuum: SQLite can only reclaim pages that
// are no longer pinned by the WAL, and both operations are best-effort with
// failures recorded on the backend rather than sent to the caller.
func (s *Store) maintain(ctx context.Context, sweep, reclaim bool) {
	if sweep {
		_ = s.sweep(ctx)
	}
	_ = s.checkpoint(ctx)
	if reclaim {
		_ = s.incrementalVacuum(ctx)
	}
}

// checkpoint records a successful passive WAL checkpoint. A busy result is
// expected when a reader is holding a snapshot; it is not a backend failure and
// the next flush or maintenance pass will retry it.
func (s *Store) checkpoint(ctx context.Context) error {
	var busy, logFrames, checkpointedFrames int
	if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		s.fail(err)
		return err
	}
	if !checkpointCompleted(busy, logFrames, checkpointedFrames) {
		return nil
	}

	s.mu.Lock()
	s.lastCheckpointAt = time.Now().UTC()
	s.mu.Unlock()
	return nil
}

func checkpointCompleted(busy, logFrames, checkpointedFrames int) bool {
	return busy == 0 && logFrames >= 0 && checkpointedFrames >= 0 && logFrames == checkpointedFrames
}

// incrementalVacuum reclaims at most the configured number of pages. The
// numeric pragma argument cannot be bound through database/sql, but it is an
// integer resolved from validated configuration, never request data.
func (s *Store) incrementalVacuum(ctx context.Context) error {
	pages := s.opts.IncrementalVacuumPages
	if pages <= 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages)); err != nil { //nolint:gosec // G201: pages is an internal validated integer
		s.fail(err)
		return err
	}
	return nil
}

// sweep enforces both retention bounds: rows older than Retention are deleted
// first, then, independently, any excess over MaxRows is deleted oldest-first.
// The two are independent because MaxRows exists specifically to bound the
// disk even when a traffic flood would keep every row inside the retention
// window (see schema.go's migration comment on why seq is the stable
// oldest-first tiebreaker).
func (s *Store) sweep(ctx context.Context) error {
	cutoff := timeToDB(s.opts.Now().Add(-s.opts.Retention))
	if _, err := s.db.ExecContext(ctx, "DELETE FROM flows WHERE time < ?", cutoff); err != nil {
		s.fail(err)
		return err
	}

	if s.opts.MaxRows > 0 {
		var count int64
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM flows").Scan(&count); err != nil {
			s.fail(err)
			return err
		}
		if excess := count - s.opts.MaxRows; excess > 0 {
			if _, err := s.db.ExecContext(ctx,
				"DELETE FROM flows WHERE seq IN (SELECT seq FROM flows ORDER BY time ASC, seq ASC LIMIT ?)",
				excess,
			); err != nil {
				s.fail(err)
				return err
			}
		}
	}
	return nil
}

// Stats implements flowstore.Store. It must never panic: a read failure here
// degrades to an unhealthy Backend rather than propagating an error, because
// the admin status page must still render around a broken backend.
func (s *Store) Stats() flowstore.Stats {
	s.mu.Lock()
	recorded, drops, failure := s.recorded, s.drops, s.failure
	s.mu.Unlock()

	backend := flowstore.Backend{
		Kind:       flowstore.BackendSQLite,
		Path:       s.path,
		Healthy:    failure == nil,
		Queued:     len(s.queue),
		QueueDrops: drops,
	}
	if failure != nil {
		backend.Error = failure.Error()
	}

	st := flowstore.Stats{
		Observations: recorded,
		Truncated:    drops,
	}

	ctx, cancel := s.queryCtx()
	defer cancel()

	var rows int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM flows").Scan(&rows); err != nil {
		backend.Healthy = false
		if backend.Error == "" {
			backend.Error = err.Error()
		}
	} else {
		backend.Rows = rows
	}

	var minTime, maxTime sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT MIN(time), MAX(time) FROM flows").Scan(&minTime, &maxTime); err != nil {
		backend.Healthy = false
		if backend.Error == "" {
			backend.Error = err.Error()
		}
	} else {
		if minTime.Valid {
			st.Earliest = dbToTime(minTime.Int64)
		}
		if maxTime.Valid {
			st.Latest = dbToTime(maxTime.Int64)
		}
	}

	if fi, err := os.Stat(s.path); err == nil {
		backend.SizeBytes = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		backend.JournalSizeBytes = fi.Size()
	}

	s.mu.Lock()
	backend.LastCheckpointAt = s.lastCheckpointAt
	s.mu.Unlock()

	st.Backend = backend
	return st
}
