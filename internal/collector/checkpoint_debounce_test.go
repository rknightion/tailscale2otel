package collector

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileStore_DebouncedConcurrentSetsCoalesceAndFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")

	var writes atomic.Int64
	orig := writeAndSync
	writeAndSync = func(f *os.File, data []byte) error {
		writes.Add(1)
		return orig(f, data)
	}
	t.Cleanup(func() { writeAndSync = orig })

	store, err := NewFileStore(path, WithWriteDebounce(time.Hour))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	flusher, ok := store.(interface{ Flush() error })
	if !ok {
		t.Fatal("debounced file store does not expose Flush")
	}

	const writers = 8
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.Set("cursor", time.Unix(int64(i+1), 0).UTC()); err != nil {
				t.Errorf("Set(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if got := writes.Load(); got != 0 {
		t.Fatalf("debounced Set persisted %d times before Flush, want 0", got)
	}
	if err := flusher.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("coalesced Sets persisted %d times, want 1", got)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, ok := reopened.Get("cursor"); !ok || got.IsZero() {
		t.Fatalf("reopened cursor = %v/%v, want the latest persisted value", got, ok)
	}
}

func TestFileStore_DebounceFlushReturnsPersistError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	store, err := NewFileStore(path, WithWriteDebounce(time.Hour))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Set("cursor", time.Unix(1, 0).UTC()); err != nil {
		t.Fatalf("Set: %v", err)
	}

	boom := errors.New("simulated disk failure")
	orig := writeAndSync
	writeAndSync = func(*os.File, []byte) error { return boom }
	t.Cleanup(func() { writeAndSync = orig })
	flusher := store.(interface{ Flush() error })
	if err := flusher.Flush(); !errors.Is(err, boom) {
		t.Fatalf("Flush error = %v, want %v", err, boom)
	}
}
