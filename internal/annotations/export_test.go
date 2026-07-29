package annotations

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
)

// NewDedupeStoreForTest exposes the dedupe set to the external test package. A
// nil store is the memory-only degraded mode.
func NewDedupeStoreForTest(store collector.CheckpointStore, retention time.Duration) *dedupeStore {
	return newDedupeStore(store, retention)
}

// NewDedupeStoreAtPathForTest opens a file-backed dedupe set, so a test can
// assert what actually survives a restart rather than what a fake remembers.
func NewDedupeStoreAtPathForTest(t *testing.T, path string, retention time.Duration) *dedupeStore {
	t.Helper()
	store, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("open dedupe store at %s: %v", path, err)
	}
	return newDedupeStore(store, retention)
}
