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

// --- #491: startup sweep of staging files orphaned by a hard kill -----------

// stagingName builds a name of exactly the shape os.CreateTemp produces for the
// pattern writeFileAtomic uses on path, so the sweep tests exercise the real
// filename shape rather than a guess at it.
func stagingName(t *testing.T, path, random string) string {
	t.Helper()
	prefix, suffix := stagingNameBounds(path)
	return filepath.Join(filepath.Dir(path), prefix+random+suffix)
}

// writeAged creates name with content and backdates its mtime by age, so a test
// can control "how old is this file" without sleeping.
func writeAged(t *testing.T, name, content string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	backdate(t, name, age)
}

// backdate sets name's atime/mtime to age in the past.
func backdate(t *testing.T, name string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	if err := os.Chtimes(name, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
}

func mustExist(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(name); err != nil {
		t.Fatalf("%s should still exist: %v", name, err)
	}
}

func mustNotExist(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(name); !os.IsNotExist(err) {
		t.Fatalf("%s should have been swept (lstat err = %v)", name, err)
	}
}

// TestSweepStaleStagingFiles_RemovesOrphanOlderThanThreshold pins #491's core
// claim: a staging file left behind by a SIGKILL between os.CreateTemp and
// os.Rename is reclaimed once it is older than the threshold.
func TestSweepStaleStagingFiles_RemovesOrphanOlderThanThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")
	orphan := stagingName(t, path, "2451910763")
	writeAged(t, orphan, `{"flowlogs":"2026-01-01T00:00:00Z"}`, stagingFileMaxAge+time.Minute)

	removed, err := sweepStaleStagingFiles(path)
	if err != nil {
		t.Fatalf("sweepStaleStagingFiles: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	mustNotExist(t, orphan)
}

// TestSweepStaleStagingFiles_KeepsRecentStagingFile is the safety property the
// whole design turns on (#491): a staging file young enough to plausibly belong
// to a CONCURRENT instance's in-flight save must never be deleted. Without the
// age guard the sweep would destroy another writer's replacement mid-flight.
func TestSweepStaleStagingFiles_KeepsRecentStagingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	// Just created (in-flight right now), and one a minute inside the threshold.
	inFlight := stagingName(t, path, "1013310937")
	if err := os.WriteFile(inFlight, []byte(`{"cursor":"in flight"}`), 0o600); err != nil {
		t.Fatalf("write in-flight staging file: %v", err)
	}
	nearThreshold := stagingName(t, path, "3877215044")
	writeAged(t, nearThreshold, `{"cursor":"nearly stale"}`, stagingFileMaxAge-time.Minute)

	removed, err := sweepStaleStagingFiles(path)
	if err != nil {
		t.Fatalf("sweepStaleStagingFiles: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0: the sweep deleted a concurrent writer's staging file", removed)
	}
	mustExist(t, inFlight)
	mustExist(t, nearThreshold)
	got, err := os.ReadFile(inFlight)
	if err != nil || string(got) != `{"cursor":"in flight"}` {
		t.Fatalf("in-flight staging file = %q/%v, want it untouched", got, err)
	}
}

// TestSweepStaleStagingFiles_NeverTouchesTheCheckpointOrBystanders pins that the
// match is scoped to exactly the names writeFileAtomic creates: not the
// checkpoint itself, not the historical predictable "<path>.tmp" sibling, not
// another file's staging name, and not a directory that happens to match. Every
// bystander here is backdated well past the threshold, so only the name/type
// scoping can save it.
func TestSweepStaleStagingFiles_NeverTouchesTheCheckpointOrBystanders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")
	const checkpointContent = `{"flowlogs":"2026-07-25T00:00:00Z"}`

	writeAged(t, path, checkpointContent, 48*time.Hour)

	bystanders := []string{
		filepath.Join(dir, "checkpoints.json.tmp"),      // the historical predictable path
		filepath.Join(dir, ".checkpoints.json.tmp"),     // matching affixes, empty random part
		filepath.Join(dir, ".checkpoints.json.9.tmp.1"), // suffix not at the end
		filepath.Join(dir, ".other.json.9.tmp"),         // another file's staging name
		filepath.Join(dir, "checkpoints.json.corrupt"),  // the corrupt-file rename target
		filepath.Join(dir, ".checkpoint-probe-9"),       // the writability probe
		filepath.Join(dir, "unrelated.txt"),
	}
	for _, b := range bystanders {
		writeAged(t, b, "bystander", 48*time.Hour)
	}
	// A directory whose name matches the staging pattern: "older than" is
	// meaningless for it and removing it is never this sweep's business.
	matchingDir := stagingName(t, path, "1717000000")
	if err := os.Mkdir(matchingDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	backdate(t, matchingDir, 48*time.Hour)

	removed, err := sweepStaleStagingFiles(path)
	if err != nil {
		t.Fatalf("sweepStaleStagingFiles: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	mustExist(t, matchingDir)
	for _, b := range bystanders {
		mustExist(t, b)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != checkpointContent {
		t.Fatalf("checkpoint after sweep = %q/%v, want %q", got, err, checkpointContent)
	}
}

// TestSweepStaleStagingFiles_DoesNotFollowSymlink pins that the sweep stats with
// Lstat, not Stat. The link's TARGET is a regular file backdated past the
// threshold, so a Stat-based sweep would classify the entry as a stale staging
// file and unlink it. writeFileAtomic creates its staging file with
// O_CREATE|O_EXCL and so can never produce a symlink; anything that is one was
// planted by somebody else and is not ours to remove.
func TestSweepStaleStagingFiles_DoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	target := filepath.Join(t.TempDir(), "sentinel")
	const sentinelContent = "someone else's file\n"
	if err := os.WriteFile(target, []byte(sentinelContent), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	link := stagingName(t, path, "4021988347")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	// Chtimes follows the link, so this backdates the TARGET: exactly the state
	// in which a Stat-based sweep sees "old regular file".
	backdate(t, link, stagingFileMaxAge+time.Hour)

	removed, err := sweepStaleStagingFiles(path)
	if err != nil {
		t.Fatalf("sweepStaleStagingFiles: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0: the sweep followed a symlink", removed)
	}
	mustExist(t, link)
	got, err := os.ReadFile(target)
	if err != nil || string(got) != sentinelContent {
		t.Fatalf("symlink target = %q/%v, want %q", got, err, sentinelContent)
	}
}

// TestNewFileStore_SweepsStaleStagingFilesOnOpen pins that the sweep actually
// runs at startup, and that it neither loses the checkpoint's contents nor
// disturbs a recent staging file on the way through.
func TestNewFileStore_SweepsStaleStagingFilesOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	seed, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	want := time.Unix(1717000123, 0).UTC()
	if err := seed.Set("flowlogs", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	backdate(t, path, 48*time.Hour) // an idle checkpoint is legitimately old

	stale := stagingName(t, path, "2811994001")
	writeAged(t, stale, "half-written", stagingFileMaxAge+time.Minute)
	fresh := stagingName(t, path, "2811994002")
	writeAged(t, fresh, "another instance is mid-save", time.Minute)

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if at, ok := reopened.Get("flowlogs"); !ok || !at.Equal(want) {
		t.Fatalf("reopened checkpoint = %v/%v, want %v", at, ok, want)
	}
	mustNotExist(t, stale)
	mustExist(t, fresh)
	mustExist(t, path)
}

// TestNewFileStore_SweepFailureIsNonFatal pins that an unreadable checkpoint
// directory degrades to a logged warning: the store still opens and still
// persists, matching how the rest of the checkpoint path degrades rather than
// blocking startup.
func TestNewFileStore_SweepFailureIsNonFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	boom := errors.New("simulated unreadable checkpoint directory")
	orig := readDirForSweep
	readDirForSweep = func(string) ([]os.DirEntry, error) { return nil, boom }
	t.Cleanup(func() { readDirForSweep = orig })

	if _, err := sweepStaleStagingFiles(path); !errors.Is(err, boom) {
		t.Fatalf("sweepStaleStagingFiles err = %v, want %v", err, boom)
	}

	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore must not fail on a sweep error, got %v", err)
	}
	want := time.Unix(1717000999, 0).UTC()
	if err := s.Set("flowlogs", want); err != nil {
		t.Fatalf("Set after a failed sweep: %v", err)
	}
	if at, ok := s.Get("flowlogs"); !ok || !at.Equal(want) {
		t.Fatalf("Get = %v/%v, want %v", at, ok, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checkpoint not persisted after a failed sweep: %v", err)
	}
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
