package collector_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
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
