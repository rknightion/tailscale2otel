package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestCheckpointStore_CorruptFileDegrades pins #69: a corrupt checkpoint file is
// renamed aside and the store starts empty (effective "file"), instead of a fatal
// error that crash-loops startup.
func TestCheckpointStore_CorruptFileDegrades(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")
	if err := os.WriteFile(path, []byte("{{ corrupt"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	cfg := &config.Config{}
	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.FilePath = path

	store, effective, err := checkpointStore(cfg, discardLogger())
	if err != nil {
		t.Fatalf("checkpointStore returned error, want graceful degrade: %v", err)
	}
	if effective != "file" {
		t.Errorf("effective = %q, want file (dir is writable)", effective)
	}
	if store == nil || len(store.Keys()) != 0 {
		t.Fatalf("store should start empty; keys=%v", store.Keys())
	}
	if _, statErr := os.Stat(path + ".corrupt"); statErr != nil {
		t.Errorf("corrupt file was not renamed aside: %v", statErr)
	}
	// The fresh store must be writable (persist survives).
	if err := store.Set("flowlogs", time.Unix(1, 0)); err != nil {
		t.Errorf("post-degrade Set: %v", err)
	}
}

// TestCheckpointStore_UnwritableReportsMemory pins the #69 effective-store report:
// an unwritable path degrades to memory and reports "memory", not "file".
func TestCheckpointStore_UnwritableReportsMemory(t *testing.T) {
	cfg := &config.Config{}
	cfg.Checkpoint.Store = "file"
	// A path under a file (not a dir) can't be made writable as a directory.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	cfg.Checkpoint.FilePath = filepath.Join(f, "sub", "checkpoints.json")
	_, effective, err := checkpointStore(cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if effective != "memory" {
		t.Errorf("effective = %q, want memory", effective)
	}
}

// fakeWindow is a minimal WindowCollector for the migration tests.
type fakeWindow struct{ name string }

func (f fakeWindow) Name() string                   { return f.name }
func (f fakeWindow) DefaultInterval() time.Duration { return time.Minute }
func (fakeWindow) CollectWindow(context.Context, time.Time, time.Time, telemetry.Emitter) (time.Time, error) {
	return time.Time{}, nil
}
func (fakeWindow) Lag() time.Duration { return 0 }

func appWithWindowRuntimes(store collector.CheckpointStore, names ...string) *App {
	a := &App{store: store}
	for _, n := range names {
		reg := collector.NewRegistry()
		reg.RegisterWindow(fakeWindow{"flowlogs"}, time.Minute, time.Minute, time.Hour)
		a.runtimes = append(a.runtimes, &tailnetRuntime{name: n, registry: reg})
	}
	return a
}

// TestMigrateCheckpointKeys_MultiToSingle pins #105: a namespaced key migrates to
// the bare key when the deployment shrinks to single-tailnet mode.
func TestMigrateCheckpointKeys_MultiToSingle(t *testing.T) {
	store := collector.NewMemoryStore()
	hwm := time.Unix(1000, 0).UTC()
	_ = store.Set("acme/flowlogs", hwm)

	a := appWithWindowRuntimes(store, "acme") // 1 runtime => single mode => bare key
	a.migrateCheckpointKeys(discardLogger())

	got, ok := store.Get("flowlogs")
	if !ok || !got.Equal(hwm) {
		t.Fatalf("cursor not migrated to bare key: got=%v ok=%v", got, ok)
	}
	if _, ok := store.Get("acme/flowlogs"); ok {
		t.Errorf("legacy namespaced key not removed")
	}
}

// TestMigrateCheckpointKeys_SingleToMulti pins #105: a bare key seeds the first
// tailnet on a grow-to-multi transition (deterministic; the rest cold-start).
func TestMigrateCheckpointKeys_SingleToMulti(t *testing.T) {
	store := collector.NewMemoryStore()
	hwm := time.Unix(2000, 0).UTC()
	_ = store.Set("flowlogs", hwm)

	a := appWithWindowRuntimes(store, "alpha", "beta") // 2 runtimes => multi
	a.migrateCheckpointKeys(discardLogger())

	got, ok := store.Get("alpha/flowlogs")
	if !ok || !got.Equal(hwm) {
		t.Fatalf("first tailnet did not adopt the bare cursor: got=%v ok=%v", got, ok)
	}
	if _, ok := store.Get("flowlogs"); ok {
		t.Errorf("bare key not removed after migration")
	}
	if _, ok := store.Get("beta/flowlogs"); ok {
		t.Errorf("second tailnet should cold-start, not adopt a cursor")
	}
}

// TestMigrateCheckpointKeys_AmbiguousColdStarts pins #105: when two legacy keys
// could match one collector, it declines to guess and leaves them as strays.
func TestMigrateCheckpointKeys_AmbiguousColdStarts(t *testing.T) {
	store := collector.NewMemoryStore()
	_ = store.Set("old1/flowlogs", time.Unix(1, 0))
	_ = store.Set("old2/flowlogs", time.Unix(2, 0))

	a := appWithWindowRuntimes(store, "acme") // single mode: current key "flowlogs"
	a.migrateCheckpointKeys(discardLogger())

	if _, ok := store.Get("flowlogs"); ok {
		t.Errorf("ambiguous migration should not adopt a cursor")
	}
	// Both stray keys are left in place (logged, not deleted).
	if _, ok := store.Get("old1/flowlogs"); !ok {
		t.Errorf("stray key old1 should be left in place")
	}
}
