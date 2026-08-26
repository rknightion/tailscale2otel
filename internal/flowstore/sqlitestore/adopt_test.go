package sqlitestore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLegacyDB builds a database in the pre-hardening layout: the unqualified
// flows-<slug>.db filename, the real schema, no tailnet identity row, and rows
// worth keeping. identity, when non-empty, stamps an identity row so the
// foreign-owner and already-stamped paths can be exercised.
func writeLegacyDB(t *testing.T, dir, tailnet, identity string, rows int) string {
	t.Helper()
	path := filepath.Join(dir, legacyDBFileName(tailnet))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy fixture: %v", err)
	}
	for _, stmt := range migrations {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatalf("set legacy version: %v", err)
	}
	for i := range rows {
		if _, err := db.Exec("INSERT INTO flows(time, src_addr) VALUES(?, ?)", i, "10.0.0.1"); err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
	}
	if identity != "" {
		if _, err := db.Exec("INSERT INTO metadata(key, value) VALUES('tailnet', ?)", identity); err != nil {
			t.Fatalf("stamp legacy identity: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy fixture: %v", err)
	}
	return path
}

func countFlows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM flows").Scan(&n); err != nil {
		t.Fatalf("count flows in %s: %v", path, err)
	}
	return n
}

func storedIdentity(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = db.Close() }()
	var v string
	if err := db.QueryRow("SELECT value FROM metadata WHERE key = 'tailnet'").Scan(&v); err != nil {
		t.Fatalf("read identity from %s: %v", path, err)
	}
	return v
}

func TestAdoptLegacyDatabaseKeepsRowsAndStampsIdentity(t *testing.T) {
	dir := t.TempDir()
	tailnet := "acme.example"
	legacy := writeLegacyDB(t, dir, tailnet, "", 3)

	res, err := AdoptLegacyDatabase(context.Background(), dir, tailnet)
	if err != nil {
		t.Fatalf("AdoptLegacyDatabase: %v", err)
	}
	if !res.Adopted {
		t.Errorf("Adopted = false, want true (%+v)", res)
	}

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy file still present after adoption, stat err = %v", err)
	}
	adopted := filepath.Join(dir, dbFileName(tailnet))
	if res.Path != adopted {
		t.Errorf("Path = %q, want %q", res.Path, adopted)
	}
	if got := countFlows(t, adopted); got != 3 {
		t.Errorf("rows after adoption = %d, want 3 — adoption must keep the history, not start fresh", got)
	}
	if got := storedIdentity(t, adopted); got != tailnet {
		t.Errorf("stored identity = %q, want %q", got, tailnet)
	}

	// The whole point: the store the app builds must now open it.
	store, err := Open(Options{Dir: dir, Tailnet: tailnet})
	if err != nil {
		t.Fatalf("Open after adoption: %v", err)
	}
	_ = store.Close()
}

func TestAdoptLegacyDatabaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	tailnet := "acme.example"
	writeLegacyDB(t, dir, tailnet, "", 2)

	if _, err := AdoptLegacyDatabase(context.Background(), dir, tailnet); err != nil {
		t.Fatalf("first adoption: %v", err)
	}
	res, err := AdoptLegacyDatabase(context.Background(), dir, tailnet)
	if err != nil {
		t.Fatalf("second adoption must be a no-op, got: %v", err)
	}
	if res.Adopted {
		t.Errorf("second adoption reported Adopted = true, want false")
	}
	if got := countFlows(t, filepath.Join(dir, dbFileName(tailnet))); got != 2 {
		t.Errorf("rows after second adoption = %d, want 2", got)
	}
}

func TestAdoptLegacyDatabaseResumesAfterAnInterruptedRun(t *testing.T) {
	// Identity is stamped before the rename, so a crash between the two leaves a
	// legacy file already carrying the right identity. Re-running must finish
	// the job rather than refuse it.
	dir := t.TempDir()
	tailnet := "acme.example"
	writeLegacyDB(t, dir, tailnet, tailnet, 4)

	res, err := AdoptLegacyDatabase(context.Background(), dir, tailnet)
	if err != nil {
		t.Fatalf("AdoptLegacyDatabase: %v", err)
	}
	if !res.Adopted {
		t.Errorf("Adopted = false, want true")
	}
	if got := countFlows(t, filepath.Join(dir, dbFileName(tailnet))); got != 4 {
		t.Errorf("rows = %d, want 4", got)
	}
}

func TestAdoptLegacyDatabaseRefusesAnotherTailnetsDatabase(t *testing.T) {
	dir := t.TempDir()
	tailnet := "acme.example"
	legacy := writeLegacyDB(t, dir, tailnet, "other.example", 1)

	_, err := AdoptLegacyDatabase(context.Background(), dir, tailnet)
	if err == nil || !strings.Contains(err.Error(), "other.example") {
		t.Fatalf("error = %v, want a refusal naming the owning tailnet", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("legacy file was moved despite refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, dbFileName(tailnet))); !os.IsNotExist(err) {
		t.Errorf("adopted path created despite refusal")
	}
}

func TestAdoptLegacyDatabaseRefusesWhenBothExist(t *testing.T) {
	dir := t.TempDir()
	tailnet := "acme.example"
	legacy := writeLegacyDB(t, dir, tailnet, "", 1)
	store, err := Open(Options{Dir: dir, Tailnet: tailnet})
	if err == nil {
		_ = store.Close()
		t.Fatal("Open unexpectedly succeeded with a legacy file present")
	}
	// Create the qualified file independently so both layouts coexist.
	if err := os.WriteFile(filepath.Join(dir, dbFileName(tailnet)), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = AdoptLegacyDatabase(context.Background(), dir, tailnet)
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("error = %v, want an ambiguity refusal", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("legacy file was moved despite refusal: %v", err)
	}
}

func TestAdoptLegacyDatabaseReportsNothingToDo(t *testing.T) {
	dir := t.TempDir()
	res, err := AdoptLegacyDatabase(context.Background(), dir, "acme.example")
	if err != nil {
		t.Fatalf("AdoptLegacyDatabase on an empty directory: %v", err)
	}
	if res.Adopted {
		t.Errorf("Adopted = true on an empty directory, want false")
	}
}

// AC2: the refusal an operator actually hits must name the way out, not tell
// them to verify ownership with no instructions.
func TestOpenRefusalNamesTheAdoptionCommand(t *testing.T) {
	dir := t.TempDir()
	tailnet := "acme.example"
	writeLegacyDB(t, dir, tailnet, "", 1)

	_, err := Open(Options{Dir: dir, Tailnet: tailnet})
	if err == nil {
		t.Fatal("Open accepted a legacy database")
	}
	if !strings.Contains(err.Error(), "-adopt-flow-db") {
		t.Errorf("refusal does not name the adoption command: %v", err)
	}
}

func TestMoveDatabaseFilesRefusesAnUnCheckpointedWAL(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "flows-acme-example.db")
	path := filepath.Join(dir, "flows-acme-example-deadbeef.db")
	if err := os.WriteFile(legacy, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy+"-wal", []byte("committed frames"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := moveDatabaseFiles(legacy, path)
	if err == nil || !strings.Contains(err.Error(), "write-ahead log") {
		t.Fatalf("error = %v, want a refusal naming the write-ahead log", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("database was moved despite the live WAL: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("destination created despite the live WAL")
	}
}

func TestMoveDatabaseFilesRollsBackWhenASidecarCannotMove(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "flows-acme-example.db")
	path := filepath.Join(dir, "flows-acme-example-deadbeef.db")
	if err := os.WriteFile(legacy, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Empty, so it passes the live-WAL guard but still has to be moved.
	if err := os.WriteFile(legacy+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory at the destination makes that one rename fail on
	// both Linux and macOS.
	if err := os.MkdirAll(filepath.Join(path+"-wal", "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := moveDatabaseFiles(legacy, path); err == nil {
		t.Fatal("moveDatabaseFiles succeeded despite an unmovable sidecar")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("database was not rolled back to %s: %v", legacy, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("database left at the destination after a failed move")
	}
}
