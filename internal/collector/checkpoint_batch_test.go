package collector_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
)

func TestUpdateCheckpointBatchPersistsNamespacedUpdatesAndDeletes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	store, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	base := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	if err := store.Set("other/cursor", base); err != nil {
		t.Fatalf("seed other namespace: %v", err)
	}
	ns := collector.Namespaced(store, "acme")
	if err := collector.UpdateCheckpointBatch(ns, map[string]time.Time{
		"flowlogs":          base.Add(time.Minute),
		"flowlogs/seen/old": base.Add(-time.Minute),
	}, nil); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if err := collector.UpdateCheckpointBatch(ns, map[string]time.Time{
		"flowlogs":          base.Add(2 * time.Minute),
		"flowlogs/seen/new": base.Add(time.Hour),
	}, []string{"flowlogs/seen/old"}); err != nil {
		t.Fatalf("update batch: %v", err)
	}

	reopened, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	for key, want := range map[string]time.Time{
		"other/cursor":           base,
		"acme/flowlogs":          base.Add(2 * time.Minute),
		"acme/flowlogs/seen/new": base.Add(time.Hour),
	} {
		if got, ok := reopened.Get(key); !ok || !got.Equal(want) {
			t.Errorf("%s = %v/%v, want %v", key, got, ok, want)
		}
	}
	if _, ok := reopened.Get("acme/flowlogs/seen/old"); ok {
		t.Error("deleted namespaced key survived batch persistence")
	}
}
