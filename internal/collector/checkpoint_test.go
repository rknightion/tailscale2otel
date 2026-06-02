package collector_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
)

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
