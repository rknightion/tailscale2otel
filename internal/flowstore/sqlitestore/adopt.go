package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// adoptCommand is the operator-facing invocation that runs AdoptLegacyDatabase.
// Every refusal that a legacy database can produce quotes this, so the message
// an operator actually hits carries the way out rather than an instruction to
// "verify ownership" with nothing to act on.
const adoptCommand = "-adopt-flow-db <tailnet>"

// AdoptResult reports what AdoptLegacyDatabase found and did.
type AdoptResult struct {
	// Adopted is true only when this call moved a legacy database into place.
	// A directory that was already adopted, or never had one, reports false
	// with no error so re-running is safe.
	Adopted bool
	// Path is the digest-qualified path the store reads, whether or not this
	// call was the one that created it.
	Path string
	// Legacy is the pre-hardening path that was considered.
	Legacy string
	// Rows is how many flow rows came across, set only when Adopted.
	Rows int64
}

// AdoptLegacyDatabase claims a pre-hardening flow database for tailnet and
// moves it to the digest-qualified name Open expects.
//
// Open deliberately refuses a legacy `flows-<slug>.db` on its own: the
// unqualified filename is attacker-influenceable and carries no proof of which
// tailnet its user identities belong to, so a service that adopted one
// automatically could be steered into serving one tailnet's flows as another's.
// The claim therefore has to be an explicit operator act, which is what this
// function is — the operator names the tailnet, asserting the ownership the
// filename cannot.
//
// It is idempotent and crash-safe in that order: the identity row is stamped
// BEFORE the rename, so an interruption between the two leaves a legacy file
// already carrying the right identity, and re-running finishes the move. The
// reverse order would leave a qualified file with no identity, which Open
// refuses outright and which nothing could then repair.
func AdoptLegacyDatabase(ctx context.Context, dir, tailnet string) (AdoptResult, error) {
	if dir == "" {
		return AdoptResult{}, errors.New("sqlitestore: flows.store.directory is empty")
	}
	if tailnet == "" {
		return AdoptResult{}, errors.New("sqlitestore: adoption needs the tailnet that owns the database")
	}

	legacy := filepath.Join(dir, legacyDBFileName(tailnet))
	path := filepath.Join(dir, dbFileName(tailnet))
	res := AdoptResult{Path: path, Legacy: legacy}

	legacyExists, err := regularFileExists(legacy)
	if err != nil {
		return res, err
	}
	qualifiedExists, err := regularFileExists(path)
	if err != nil {
		return res, err
	}

	switch {
	case legacyExists && qualifiedExists:
		return res, fmt.Errorf(
			"sqlitestore: both %s and %s exist for tailnet %q; refusing an ambiguous adoption — keep the one you want and move the other aside",
			legacy, path, tailnet)
	case !legacyExists:
		// Either already adopted or never persisted. Both are a no-op, not an
		// error: the operator may well be running this across a fleet.
		return res, nil
	}

	rows, err := stampLegacyIdentity(ctx, legacy, tailnet)
	if err != nil {
		return res, err
	}

	// Re-check immediately before the move. The window is small and this is an
	// operator-run one-shot rather than a service path, but silently writing
	// over a database that appeared in the meantime is not a failure mode worth
	// leaving open.
	if again, err := regularFileExists(path); err != nil {
		return res, err
	} else if again {
		return res, fmt.Errorf("sqlitestore: %s appeared while adopting %s; refusing to overwrite it", path, legacy)
	}

	if err := moveDatabaseFiles(legacy, path); err != nil {
		return res, err
	}

	res.Adopted = true
	res.Rows = rows
	return res, nil
}

// moveDatabaseFiles moves a closed SQLite database and any sidecars it left
// behind, all-or-nothing.
//
// Two things make a naive rename unsafe. A WAL still holding committed frames
// belongs to a database that was not checkpointed, and moving the main file out
// from under it strands those frames — the reopened database would silently be
// missing its most recent writes. And moving the main file successfully but
// failing on a sidecar would leave the pair split across two names with no way
// back. So: refuse a live WAL outright, and undo the main move if a sidecar
// move fails.
//
// In the normal path there is nothing to do here. stampLegacyIdentity closes
// the database cleanly, which checkpoints the WAL into the main file and
// removes both sidecars; a non-empty WAL surviving that means something else
// still has the database open.
func moveDatabaseFiles(legacy, path string) error {
	if info, err := os.Lstat(legacy + "-wal"); err == nil && info.Size() > 0 {
		return fmt.Errorf(
			"sqlitestore: %s still has an un-checkpointed write-ahead log (%s, %d bytes), which means something still has the database open; "+
				"stop tailscale2otel and any other reader, then run the adoption again",
			legacy, legacy+"-wal", info.Size())
	}

	if err := os.Rename(legacy, path); err != nil {
		return fmt.Errorf("sqlitestore: move %s to %s: %w", legacy, path, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(legacy + suffix); err != nil {
			continue
		}
		if err := os.Rename(legacy+suffix, path+suffix); err != nil {
			// Put the database back so the operator is left exactly where they
			// started rather than with a half-moved pair. If even that fails
			// there is nothing further to try, so report both.
			if undo := os.Rename(path, legacy); undo != nil {
				return fmt.Errorf(
					"sqlitestore: move %s to %s failed (%w) and %s could not be restored to %s (%w); the database is at %s",
					legacy+suffix, path+suffix, err, path, legacy, undo, path)
			}
			return fmt.Errorf("sqlitestore: move %s to %s: %w", legacy+suffix, path+suffix, err)
		}
	}
	return nil
}

// stampLegacyIdentity opens the legacy database under the same no-follow guard
// Open uses, brings its schema forward, writes the tailnet identity row if it
// has none, and reports the row count. It refuses a database that already
// names a different owner.
func stampLegacyIdentity(ctx context.Context, legacy, tailnet string) (int64, error) {
	guard, err := openDatabaseGuard(legacy, true)
	if err != nil {
		return 0, err
	}
	defer func() { _ = guard.Close() }()

	dsn := legacy + "?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, fmt.Errorf("sqlitestore: open %s: %w", legacy, err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return 0, fmt.Errorf("sqlitestore: connect %s: %w", legacy, err)
	}
	if err := verifyDatabaseGuard(legacy, guard); err != nil {
		return 0, err
	}
	if err := migrate(ctx, db); err != nil {
		return 0, err
	}

	var stored string
	err = db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key = 'tailnet'").Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := db.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('tailnet', ?)", tailnet); err != nil {
			return 0, fmt.Errorf("sqlitestore: stamp tailnet identity on %s: %w", legacy, err)
		}
	case err != nil:
		return 0, fmt.Errorf("sqlitestore: read tailnet identity from %s: %w", legacy, err)
	case stored != tailnet:
		return 0, fmt.Errorf(
			"sqlitestore: %s already belongs to tailnet %q, not %q; refusing to re-label another tailnet's flow history",
			legacy, stored, tailnet)
	}

	var rows int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM flows").Scan(&rows); err != nil {
		return 0, fmt.Errorf("sqlitestore: count rows in %s: %w", legacy, err)
	}
	if err := verifyDatabaseGuard(legacy, guard); err != nil {
		return 0, err
	}
	if err := db.Close(); err != nil {
		return 0, fmt.Errorf("sqlitestore: close %s: %w", legacy, err)
	}
	return rows, nil
}

// regularFileExists reports whether path is an existing regular file. A
// symlink or any other node type is an error rather than a false: those are
// exactly the shapes the qualified-filename hardening exists to refuse, and
// treating one as "absent" would let adoption create a file on the far side of
// it.
func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("sqlitestore: inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("sqlitestore: %s is not a regular database file", path)
	}
	return true, nil
}
