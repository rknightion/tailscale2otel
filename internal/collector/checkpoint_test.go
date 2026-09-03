package collector_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
)

// TestFileStore_CorruptFileReportsSentinel pins #69: a checkpoint file that fails
// to decode returns ErrCorruptCheckpoint (so the caller can degrade to an empty
// checkpoint) rather than an opaque error that crash-loops startup.
func TestFileStore_CorruptFileReportsSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	if err := os.WriteFile(path, []byte("{ this is not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	_, err := collector.NewFileStore(path)
	if !errors.Is(err, collector.ErrCorruptCheckpoint) {
		t.Fatalf("NewFileStore on corrupt file err = %v, want ErrCorruptCheckpoint", err)
	}
}

// TestFileStore_DeleteAndKeys pins the store surface #105's migration relies on.
func TestFileStore_DeleteAndKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	s, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_ = s.Set("acme/flowlogs", time.Unix(1000, 0).UTC())
	if keys := s.Keys(); len(keys) != 1 || keys[0] != "acme/flowlogs" {
		t.Fatalf("Keys = %v, want [acme/flowlogs]", keys)
	}
	if err := s.Delete("acme/flowlogs"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s.Get("acme/flowlogs"); ok {
		t.Fatal("key present after Delete")
	}
	// The delete must be persisted (reopen sees no key).
	s2, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(s2.Keys()) != 0 {
		t.Fatalf("reopened store keys = %v, want none", s2.Keys())
	}
}

// TestFileStore_PersistDoesNotFollowSymlinkAtPredictableTempPath pins
// security:SEC-08 (#471): the historical implementation derived a predictable
// sibling temp path ("<path>.tmp") and opened it with plain O_CREATE|O_TRUNC, so
// another local principal on a shared checkpoint directory could park a symlink
// there and have the exporter write through it. Persisting must never open,
// write through, replace, or remove a pre-existing symlink at that path.
func TestFileStore_PersistDoesNotFollowSymlinkAtPredictableTempPath(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	const sentinelContent = "attacker-owned file that must not be clobbered\n"
	if err := os.WriteFile(sentinel, []byte(sentinelContent), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	path := filepath.Join(dir, "checkpoints.json")
	trap := path + ".tmp"
	if err := os.Symlink(sentinel, trap); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	s, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	want := time.Unix(1717000456, 0).UTC()
	if err := s.Set("flowlogs", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// 1. The file the symlink points at must be byte-unchanged.
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(got) != sentinelContent {
		t.Errorf("sentinel was written through the symlink:\n got %q\nwant %q", got, sentinelContent)
	}

	// 2. The symlink itself must be untouched — not renamed away, not replaced.
	li, err := os.Lstat(trap)
	if err != nil {
		t.Fatalf("pre-existing symlink at %s was removed or renamed: %v", trap, err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Errorf("pre-existing symlink at %s was replaced by a %v", trap, li.Mode().Type())
	} else if dest, err := os.Readlink(trap); err != nil || dest != sentinel {
		t.Errorf("symlink target = %q/%v, want %q", dest, err, sentinel)
	}

	// 3. The checkpoint itself must be a real regular file holding the new state.
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat checkpoint: %v", err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("checkpoint path is a %v, want a regular file", fi.Mode().Type())
	}
	reopened, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if at, ok := reopened.Get("flowlogs"); !ok || !at.Equal(want) {
		t.Fatalf("reopened checkpoint = %v/%v, want %v", at, ok, want)
	}
}

// TestFileStore_ConcurrentSetsCannotShareTempFiles pins #471: independent
// savers writing the same checkpoint path must each stage their replacement in
// a file of its own, and a reader must never observe a truncated or partial
// checkpoint while they do. Run with -race.
func TestFileStore_ConcurrentSetsCannotShareTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	const writers, rounds = 8, 25
	base := time.Unix(1717000000, 0).UTC()

	// Seed the file so the concurrent readers always have something to read.
	seed, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := seed.Set("cursor", base); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	stop := make(chan struct{})
	errs := make(chan error, writers*rounds+1)

	var writersWG, readerWG sync.WaitGroup
	for w := range writers {
		// A separate store per writer: within one store s.mu serializes saves,
		// so only independent stores exercise temp-file uniqueness.
		s, err := collector.NewFileStore(path)
		if err != nil {
			t.Fatalf("NewFileStore(writer %d): %v", w, err)
		}
		writersWG.Add(1)
		go func() {
			defer writersWG.Done()
			for r := range rounds {
				if err := s.Set("cursor", base.Add(time.Duration(w*rounds+r)*time.Second)); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	// A reader racing the writers: every observation must be a complete,
	// decodable checkpoint, never a partial one.
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := collector.NewFileStore(path); err != nil {
				errs <- err
				return
			}
		}
	}()

	writersWG.Wait()
	close(stop)
	readerWG.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent save/read: %v", err)
	}

	// The survivor must be a complete checkpoint holding one of the written values.
	final, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("final store is not a complete checkpoint: %v", err)
	}
	at, ok := final.Get("cursor")
	if !ok {
		t.Fatal("final checkpoint lost the cursor key")
	}
	if at.Before(base) || at.After(base.Add(time.Duration(writers*rounds)*time.Second)) {
		t.Fatalf("final cursor %v is outside the written range", at)
	}
	assertOnlyCheckpointFile(t, dir, path)
}

// TestFileStore_PersistedFileIsOwnerOnly pins the intended 0600 mode across the
// switch to os.CreateTemp (which creates 0600 before umask, so a strict umask
// would otherwise narrow it).
func TestFileStore_PersistedFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")
	s, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s.Set("flowlogs", time.Unix(1717000000, 0).UTC()); err != nil {
		t.Fatalf("Set: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("checkpoint mode = %04o, want 0600", got)
	}
}

// TestFileStore_SuccessfulPersistLeavesNoTempFiles pins that the randomized
// staging file is always consumed by the rename or removed.
func TestFileStore_SuccessfulPersistLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")
	s, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for i := range 5 {
		if err := s.Set("flowlogs", time.Unix(int64(1717000000+i), 0).UTC()); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	if err := s.Delete("flowlogs"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertOnlyCheckpointFile(t, dir, path)
}

// assertOnlyCheckpointFile fails if dir holds anything besides the checkpoint
// file itself — i.e. if a staging temp file was left behind.
func assertOnlyCheckpointFile(t *testing.T, dir, path string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read checkpoint dir: %v", err)
	}
	var leftovers []string
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			leftovers = append(leftovers, e.Name())
		}
	}
	if len(leftovers) > 0 {
		t.Fatalf("temp files left behind in %s: %v", dir, leftovers)
	}
}

func TestMemoryStore_GetMissing(t *testing.T) {
	s := collector.NewMemoryStore()
	if _, ok := s.Get("flowlogs"); ok {
		t.Fatal("Get on empty store returned ok=true, want false")
	}
}

func TestMemoryStore_SetThenGet(t *testing.T) {
	s := collector.NewMemoryStore()
	want := time.Unix(1717000000, 0).UTC()
	if err := s.Set("flowlogs", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok := s.Get("flowlogs")
	if !ok {
		t.Fatal("Get after Set returned ok=false")
	}
	if !got.Equal(want) {
		t.Fatalf("Get = %v, want %v", got, want)
	}
}

func TestFileStore_PersistsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	want := time.Unix(1717000123, 0).UTC()

	s1, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s1.Set("auditlogs", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// A fresh store reading the same file must see the persisted checkpoint.
	s2, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore (reopen): %v", err)
	}
	got, ok := s2.Get("auditlogs")
	if !ok {
		t.Fatal("reopened store missing the persisted checkpoint")
	}
	if !got.Equal(want) {
		t.Fatalf("persisted checkpoint = %v, want %v", got, want)
	}
}

func TestFileStore_MissingFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	s, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore on missing file should not error, got %v", err)
	}
	if _, ok := s.Get("flowlogs"); ok {
		t.Fatal("fresh file store returned a checkpoint, want none")
	}
}

// Namespaced is what keeps two tailnets' object-store cursors apart when they
// share one checkpoint file. Without it each would read the other's cursor and
// skip the other's ground.
func TestNamespaced_IsolatesAndStrips(t *testing.T) {
	base := collector.NewMemoryStore()
	one := collector.Namespaced(base, "one.example.com")
	two := collector.Namespaced(base, "two.example.com")

	at := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := one.Set("cursor", at); err != nil {
		t.Fatal(err)
	}
	if err := two.Set("cursor", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, ok := one.Get("cursor")
	if !ok || !got.Equal(at) {
		t.Errorf("one's cursor = %v/%v, want %v", got, ok, at)
	}
	if _, ok := two.Get("nothing"); ok {
		t.Error("a key that was never set resolved")
	}

	// Keys are stripped of the prefix and scoped to the namespace, so a caller
	// enumerating its own state never sees another tailnet's.
	if keys := one.Keys(); len(keys) != 1 || keys[0] != "cursor" {
		t.Errorf("one's keys = %v, want just [cursor]", keys)
	}
	// The underlying store holds both, distinctly.
	if len(base.Keys()) != 2 {
		t.Errorf("base keys = %v, want both namespaces", base.Keys())
	}

	if err := one.Delete("cursor"); err != nil {
		t.Fatal(err)
	}
	if _, ok := two.Get("cursor"); !ok {
		t.Error("deleting one namespace's key removed the other's")
	}
}

// An empty namespace is a pass-through, so a single-tailnet deployment's keys
// stay bare and keep resolving across an upgrade.
func TestNamespaced_EmptyIsAPassThrough(t *testing.T) {
	base := collector.NewMemoryStore()
	if got := collector.Namespaced(base, ""); got != base {
		t.Error("an empty namespace wrapped the store; existing keys would stop resolving")
	}
}
