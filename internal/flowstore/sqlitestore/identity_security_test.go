package sqlitestore

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDBFileNameIsCollisionResistant(t *testing.T) {
	names := []string{"foo.bar", "foo-bar", "!!!", "???", "Acme.Example", "acme.example", "éxample", "e\u0301xample"}
	seen := map[string]string{}
	for _, name := range names {
		file := dbFileName(name)
		key := strings.ToLower(file)
		if prior, ok := seen[key]; ok {
			t.Fatalf("dbFileName collision: %q and %q -> %q", prior, name, file)
		}
		seen[key] = name
	}
}

func TestValidateTailnetNamesRejectsLegacyFilenameCollision(t *testing.T) {
	if err := ValidateTailnetNames([]string{"foo.bar", "foo-bar"}); err == nil {
		t.Fatal("legacy filename collision accepted")
	}
	if err := ValidateTailnetNames([]string{"foo.bar", "bar.example"}); err != nil {
		t.Fatalf("non-colliding names rejected: %v", err)
	}
}

func TestOpenRejectsDatabaseOwnedByDifferentTailnet(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(Options{Dir: dir, Tailnet: "a.example"})
	if err != nil {
		t.Fatalf("Open(a): %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close(a): %v", err)
	}
	if err := os.Rename(filepath.Join(dir, dbFileName("a.example")), filepath.Join(dir, dbFileName("b.example"))); err != nil {
		t.Fatalf("rename fixture: %v", err)
	}
	if _, err := Open(Options{Dir: dir, Tailnet: "b.example"}); err == nil || !strings.Contains(err.Error(), "tailnet identity") {
		t.Fatalf("Open(b) error = %v, want tailnet identity mismatch", err)
	}
}

func TestOpenRejectsSymlinkAtDigestQualifiedDatabasePath(t *testing.T) {
	dir := t.TempDir()
	tailnet := "acme.example"
	target := filepath.Join(t.TempDir(), "attacker.db")
	if err := os.WriteFile(target, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, dbFileName(tailnet))
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{Dir: dir, Tailnet: tailnet}); err == nil {
		t.Fatal("Open accepted a symlink at the digest-qualified database path")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "not a database" {
		t.Fatalf("symlink target was mutated: %q", got)
	}
}

func TestOpenRefusesToClaimLegacyDatabaseWithoutIdentity(t *testing.T) {
	dir := t.TempDir()
	tailnet := "acme.example"
	legacyPath := filepath.Join(dir, legacyDBFileName(tailnet))
	db, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatalf("open legacy fixture: %v", err)
	}
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatalf("create legacy flows: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatalf("set legacy version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy fixture: %v", err)
	}

	_, err = Open(Options{Dir: dir, Tailnet: tailnet})
	if err == nil || !strings.Contains(err.Error(), "cannot prove its tailnet identity") {
		t.Fatalf("Open legacy error = %v, want ambiguous identity refusal", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy path was mutated despite refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, dbFileName(tailnet))); !os.IsNotExist(err) {
		t.Fatalf("digest-qualified path unexpectedly exists, stat err=%v", err)
	}
}
