package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// schemaVersion is the current shape of the flows table. Migrations are
// forward-only and each step is applied in one transaction. A database written
// by a NEWER binary is refused rather than migrated backwards: silently reading
// a schema we do not understand would produce a plausible-looking but wrong
// flow view, which is the one outcome this whole feature must avoid.
const schemaVersion = 1

// columns is the insert column list, in the order the writer binds and the
// order scanRecent expects. Every read and write in this package derives its
// column order from here, so adding a column is one edit plus a migration.
//
// dst_port is stored even though flowstore.Recent has no such field: Query
// groups ports by flowstore.PortKey{Port, Transport, Service}, so the port must
// survive even though the raw-connection list never shows it.
var columns = []string{
	"time",
	"traffic_type",
	"transport",
	"src_addr",
	"dst_addr",
	"src_node",
	"dst_node",
	"dst_port",
	"dst_service",
	"src_user",
	"dst_user",
	"src_tags",
	"dst_tags",
	"src_os",
	"dst_os",
	"reporter_node_id",
	"reporter_trust",
	"reporter_consistency",
	"verdict",
	"reversed",
	"rule",
	"policy_version",
	"path",
	"derp_region",
	"tx_bytes",
	"rx_bytes",
	"tx_packets",
	"rx_packets",
	"flows",
}

// recentColumns is the projection RecentPage and the export path read. It is
// "seq plus everything", because the cursor is the row's seq.
var recentColumns = append([]string{"seq"}, columns...)

// migrations are applied in order for any database below schemaVersion.
//
// time is stored as Unix NANOSECONDS in an INTEGER, so ordering is exact and a
// range scan is an integer comparison. seq is the AUTOINCREMENT rowid, which
// gives RecentPage the stable ingestion-order tiebreaker its cursor needs —
// AUTOINCREMENT specifically, not a bare rowid, so a deleted row's seq is never
// reused and a cursor cannot silently skip or repeat a page after a sweep.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS flows (
		seq                  INTEGER PRIMARY KEY AUTOINCREMENT,
		time                 INTEGER NOT NULL,
		traffic_type         TEXT NOT NULL DEFAULT '',
		transport            TEXT NOT NULL DEFAULT '',
		src_addr             TEXT NOT NULL DEFAULT '',
		dst_addr             TEXT NOT NULL DEFAULT '',
		src_node             TEXT NOT NULL DEFAULT '',
		dst_node             TEXT NOT NULL DEFAULT '',
		dst_port             TEXT NOT NULL DEFAULT '',
		dst_service          TEXT NOT NULL DEFAULT '',
		src_user             TEXT NOT NULL DEFAULT '',
		dst_user             TEXT NOT NULL DEFAULT '',
		src_tags             TEXT NOT NULL DEFAULT '',
		dst_tags             TEXT NOT NULL DEFAULT '',
		src_os               TEXT NOT NULL DEFAULT '',
		dst_os               TEXT NOT NULL DEFAULT '',
		reporter_node_id     TEXT NOT NULL DEFAULT '',
		reporter_trust       TEXT NOT NULL DEFAULT '',
		reporter_consistency TEXT NOT NULL DEFAULT '',
		verdict              TEXT NOT NULL DEFAULT '',
		reversed             INTEGER NOT NULL DEFAULT 0,
		rule                 INTEGER NOT NULL DEFAULT -1,
		policy_version       TEXT NOT NULL DEFAULT '',
		path                 TEXT NOT NULL DEFAULT '',
		derp_region          TEXT NOT NULL DEFAULT '',
		tx_bytes             INTEGER NOT NULL DEFAULT 0,
		rx_bytes             INTEGER NOT NULL DEFAULT 0,
		tx_packets           INTEGER NOT NULL DEFAULT 0,
		rx_packets           INTEGER NOT NULL DEFAULT 0,
		flows                INTEGER NOT NULL DEFAULT 0
	)`,
	// Every read is time-windowed, and RecentPage pages newest-first with seq as
	// the tiebreaker, so this one index serves both the range scan and the sort.
	`CREATE INDEX IF NOT EXISTS idx_flows_time_seq ON flows (time DESC, seq DESC)`,
	// The retention sweep and the row cap both delete by oldest-first.
	`CREATE INDEX IF NOT EXISTS idx_flows_time ON flows (time)`,
}

// dbFileName derives this tailnet's filename. Tailnet names contain dots and
// may contain characters that are not safe in a path, so everything outside a
// conservative allowlist is replaced. The name is only a label — collisions are
// harmless in practice because a process serves each tailnet once — but a
// traversal in it would not be, so separators can never survive.
func dbFileName(tailnet string) string {
	var b strings.Builder
	for _, r := range tailnet {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "default"
	}
	return "flows-" + name + ".db"
}

// Open prepares this tailnet's database and starts the write-behind goroutine.
//
// It creates the directory if absent. A failure here is returned rather than
// swallowed: the operator explicitly asked for persistence by setting a path,
// so falling back to memory silently would leave them believing they had
// history they do not have.
func Open(opts Options) (*Store, error) {
	opts.applyDefaults()
	if opts.Dir == "" {
		return nil, errors.New("sqlitestore: flows.store.directory is empty")
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("sqlitestore: create %s: %w", opts.Dir, err)
	}

	path := filepath.Join(opts.Dir, dbFileName(opts.Tailnet))

	// WAL keeps the single writer from blocking readers, which matters because
	// the admin page reads while the drain goroutine writes. busy_timeout covers
	// the brief exclusive locks a checkpoint or the sweep takes. foreign_keys is
	// on for correctness even though this schema has none yet.
	dsn := path + "?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: open %s: %w", path, err)
	}

	// One writer goroutine and a handful of readers. Leaving this unbounded lets
	// modernc open a connection per concurrent reader, each with its own page
	// cache, which is a memory footprint nobody asked for.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	ctx, cancel := context.WithTimeout(context.Background(), opts.QueryTimeout)
	defer cancel()

	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{
		opts:  opts,
		db:    db,
		path:  path,
		queue: make(chan flowstore.Observation, opts.QueueSize),
		done:  make(chan struct{}),
	}

	s.wg.Add(1)
	go s.run()

	return s, nil
}

// migrate brings the database up to schemaVersion, or refuses it.
func migrate(ctx context.Context, db *sql.DB) error {
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("sqlitestore: read schema version: %w", err)
	}

	if current > schemaVersion {
		return fmt.Errorf(
			"sqlitestore: database is schema version %d but this build understands %d: "+
				"it was written by a newer tailscale2otel. Point flows.store.directory at a new "+
				"directory or upgrade, rather than letting an older build read it",
			current, schemaVersion)
	}
	if current == schemaVersion {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitestore: begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := current; i < schemaVersion; i++ {
		for _, stmt := range migrations {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("sqlitestore: migrate to v%d: %w", i+1, err)
			}
		}
	}

	// PRAGMA user_version does not accept a bind parameter.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("sqlitestore: set schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitestore: commit migration: %w", err)
	}
	return nil
}

// Close stops the writer, drains what is already queued, and releases the file.
// Draining rather than discarding matters on a clean shutdown: those rows are
// already accepted as far as the emit path is concerned.
func (s *Store) Close() error {
	select {
	case <-s.done:
		return nil // already closed
	default:
	}
	close(s.done)
	s.wg.Wait()
	return s.db.Close()
}

// queryCtx bounds one read so a wide window cannot hang the admin page.
func (s *Store) queryCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.opts.QueryTimeout)
}

// timeToDB and dbToTime are the single conversion point for the time column.
func timeToDB(t time.Time) int64 { return t.UnixNano() }
func dbToTime(n int64) time.Time { return time.Unix(0, n).UTC() }
func boolToDB(b bool) int {
	if b {
		return 1
	}
	return 0
}
