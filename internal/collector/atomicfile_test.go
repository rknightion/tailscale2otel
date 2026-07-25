package collector

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// failWriteAndSync swaps writeAndSync for one that produces exactly what a
// crash mid-write looks like — a torn prefix on disk, then an error — and
// restores the real one when the test ends.
func failWriteAndSync(t *testing.T, boom error) {
	t.Helper()
	orig := writeAndSync
	writeAndSync = func(f *os.File, data []byte) error {
		if len(data) > 4 {
			_, _ = f.Write(data[:4]) // a torn write, never flushed
		}
		return boom
	}
	t.Cleanup(func() { writeAndSync = orig })
}

// TestFileStore_FailedPersistLeavesPreviousCheckpointComplete pins #471's
// crash/restart clause: an interrupted save leaves the OLD complete checkpoint
// on disk, never a truncated one, and drops no temp file.
func TestFileStore_FailedPersistLeavesPreviousCheckpointComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	first := time.Unix(1717000000, 0).UTC()
	if err := s.Set("flowlogs", first); err != nil {
		t.Fatalf("Set: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}

	boom := errors.New("simulated disk failure")
	failWriteAndSync(t, boom)

	if err := s.Set("flowlogs", first.Add(time.Hour)); !errors.Is(err, boom) {
		t.Fatalf("Set during a failed write returned %v, want %v", err, boom)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("checkpoint file gone after a failed persist: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("checkpoint file mutated by a failed persist:\n got %q\nwant %q", after, before)
	}
	// It must still be a complete, decodable checkpoint holding the old value.
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("checkpoint is no longer decodable after a failed persist: %v", err)
	}
	if at, ok := reopened.Get("flowlogs"); !ok || !at.Equal(first) {
		t.Fatalf("reopened checkpoint = %v/%v, want the pre-failure %v", at, ok, first)
	}
	assertOnlyFile(t, dir, filepath.Base(path))
}

// TestWriteFileAtomic_FailedWriteRemovesItsOwnTempFile pins that the staging
// file created by a failed call is removed, and only that file.
func TestWriteFileAtomic_FailedWriteRemovesItsOwnTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// A bystander file at the historical predictable temp path must survive
	// untouched: cleanup removes only the file this call created.
	bystander := path + ".tmp"
	if err := os.WriteFile(bystander, []byte("not mine"), 0o600); err != nil {
		t.Fatalf("write bystander: %v", err)
	}

	boom := errors.New("simulated disk failure")
	failWriteAndSync(t, boom)

	if err := writeFileAtomic(path, []byte("{}"), 0o600); !errors.Is(err, boom) {
		t.Fatalf("writeFileAtomic = %v, want %v", err, boom)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a failed write created the destination file (stat err = %v)", err)
	}
	got, err := os.ReadFile(bystander)
	if err != nil || string(got) != "not mine" {
		t.Fatalf("bystander at the predictable temp path = %q/%v, want %q", got, err, "not mine")
	}
	assertOnlyFile(t, dir, filepath.Base(bystander))
}

// TestWriteFileAtomic_NeverUsesThePredictableTempPath pins that no staging file
// is ever created at "<path>.tmp", the name the old implementation used.
func TestWriteFileAtomic_NeverUsesThePredictableTempPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	seen := map[string]bool{}
	orig := writeAndSync
	writeAndSync = func(f *os.File, data []byte) error {
		if seen[f.Name()] {
			t.Errorf("staging file %s reused across calls", f.Name())
		}
		seen[f.Name()] = true
		if f.Name() == path+".tmp" {
			t.Errorf("staging file used the predictable path %s", f.Name())
		}
		return orig(f, data)
	}
	t.Cleanup(func() { writeAndSync = orig })

	for range 5 {
		if err := writeFileAtomic(path, []byte("{}"), 0o600); err != nil {
			t.Fatalf("writeFileAtomic: %v", err)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("saw %d distinct staging files across 5 writes, want 5", len(seen))
	}
	assertOnlyFile(t, dir, filepath.Base(path))
}

// assertOnlyFile fails if dir holds anything besides the named file.
func assertOnlyFile(t *testing.T, dir, keep string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var leftovers []string
	for _, e := range entries {
		if e.Name() != keep {
			leftovers = append(leftovers, e.Name())
		}
	}
	if len(leftovers) > 0 {
		t.Fatalf("unexpected leftover files in %s: %v", dir, leftovers)
	}
}
