package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// writeAdoptConfig writes a minimal config naming one tailnet and pointing the
// flow store at dir, and returns its path.
func writeAdoptConfig(t *testing.T, dir, tailnet string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "" +
		"tailscale:\n" +
		"  tailnet: " + tailnet + "\n" +
		"  auth:\n" +
		"    method: apikey\n" +
		"    apikey: tskey-api-test\n" +
		"flows:\n" +
		"  store:\n" +
		"    directory: " + dir + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeLegacyFlowDB builds a pre-hardening database: the unqualified filename,
// a flows table, and no tailnet identity row.
func writeLegacyFlowDB(t *testing.T, dir, legacyName string) string {
	t.Helper()
	path := filepath.Join(dir, legacyName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS flows (seq INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER NOT NULL, src_addr TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO flows(time) VALUES(1)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("build legacy fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAdoptFlowDBRequiresATailnet(t *testing.T) {
	// -adopt-flow-db= must be rejected as a mistake, NOT fall through to the
	// normal server run. Asserting on the message as well as the code matters:
	// a bare non-zero exit is also what starting the server and failing would
	// produce, so the code alone would pass for the wrong reason.
	var stdout, stderr bytes.Buffer
	code := run([]string{"-adopt-flow-db="}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "needs the tailnet") {
		t.Errorf("stderr = %q, want it to say the flag needs a tailnet", stderr.String())
	}
}

func TestAdoptFlowDBRejectsAnUnconfiguredTailnet(t *testing.T) {
	storeDir := t.TempDir()
	cfg := writeAdoptConfig(t, storeDir, "acme.example")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", cfg, "-adopt-flow-db", "typo.example"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("unconfigured tailnet accepted; stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "acme.example") {
		t.Errorf("error does not list the configured tailnets: %q", stderr.String())
	}
}

func TestAdoptFlowDBAdoptsAndIsSafeToRerun(t *testing.T) {
	storeDir := t.TempDir()
	cfg := writeAdoptConfig(t, storeDir, "acme.example")
	legacy := writeLegacyFlowDB(t, storeDir, "flows-acme-example.db")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", cfg, "-adopt-flow-db", "acme.example"}, &stdout, &stderr); code != 0 {
		t.Fatalf("adoption exit = %d, stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy file survived adoption, stat err = %v", err)
	}
	if !strings.Contains(stdout.String(), "acme.example") {
		t.Errorf("adoption reported nothing useful: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-config", cfg, "-adopt-flow-db", "acme.example"}, &stdout, &stderr); code != 0 {
		t.Fatalf("re-run exit = %d, want 0 (adoption must be safe to repeat); stderr=%q", code, stderr.String())
	}
}

func TestAdoptFlowDBRequiresAConfiguredStoreDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "" +
		"tailscale:\n" +
		"  tailnet: acme.example\n" +
		"  auth:\n" +
		"    method: apikey\n" +
		"    apikey: tskey-api-test\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", path, "-adopt-flow-db", "acme.example"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("adoption accepted a config with no flows.store.directory")
	}
	if !strings.Contains(stderr.String(), "flows.store.directory") {
		t.Errorf("error does not name the missing setting: %q", stderr.String())
	}
}
