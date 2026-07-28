package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
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
	if effective.Kind != "file" {
		t.Errorf("effective = %q, want file (dir is writable)", effective.Kind)
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
	if effective.Kind != "memory" {
		t.Errorf("effective = %q, want memory", effective.Kind)
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

// TestCheckpointStore_RelocatesUnwritableDefaultToPlatformPath pins #336.
//
// The default /var/lib/tailscale2otel is writable inside the container image
// (which pre-seeds it for uid 65532) but not for a native run: on Linux only
// root can create it, and on macOS and Windows it does not exist at all — yet
// releases ship binaries for all three. Before this, such a run degraded
// silently to in-memory checkpoints and cold-started every window collector
// from initial_lookback on each restart.
func TestCheckpointStore_RelocatesUnwritableDefaultToPlatformPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LocalAppData", home)
	t.Setenv("XDG_STATE_HOME", "")

	// Stand in for /var/lib on a non-root native run: a path that cannot be
	// created, which checkpointStore is told is the default. Injecting the
	// default is the only way to exercise this without depending on whether the
	// test runner happens to be root.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	unwritableDefault := filepath.Join(f, "sub", "checkpoints.json")

	cfg := &config.Config{}
	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.FilePath = unwritableDefault

	store, out, err := checkpointStoreWithDefault(cfg, discardLogger(), unwritableDefault)
	if err != nil {
		t.Fatalf("checkpointStore: %v", err)
	}
	if out.Kind != "file" {
		t.Fatalf("effective store = %q, want file — the platform path is writable, so "+
			"degrading to memory means the relocation did not happen", out.Kind)
	}
	want := config.DefaultCheckpointPath()
	if out.Path != want {
		t.Errorf("effective path = %q, want the platform state path %q", out.Path, want)
	}
	if out.Reason == "" {
		t.Error("relocation happened but Reason is empty; the status page would show no explanation")
	}
	if err := store.Set("flowlogs", time.Unix(1, 0)); err != nil {
		t.Errorf("relocated store is not writable: %v", err)
	}
	if _, statErr := os.Stat(want); statErr != nil {
		t.Errorf("nothing was written to the relocated path: %v", statErr)
	}
}

// TestCheckpointStore_ExplicitPathIsNeverRelocated pins the boundary. An
// operator who names a path has made a decision — very likely a mounted volume
// that is briefly absent. Silently writing their checkpoints somewhere else
// would hide a real misconfiguration and split state across two locations.
func TestCheckpointStore_ExplicitPathIsNeverRelocated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LocalAppData", home)
	t.Setenv("XDG_STATE_HOME", "")

	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	cfg := &config.Config{}
	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.FilePath = filepath.Join(f, "sub", "checkpoints.json")

	_, out, err := checkpointStore(cfg, discardLogger())
	if err != nil {
		t.Fatalf("checkpointStore: %v", err)
	}
	if out.Kind != "memory" {
		t.Errorf("effective store = %q, want memory: an explicitly configured path must "+
			"WARN and degrade, never relocate", out.Kind)
	}
	if _, statErr := os.Stat(config.DefaultCheckpointPath()); statErr == nil {
		t.Error("checkpoints were relocated to the platform path despite an explicit configured path")
	}
}

// TestCheckpointStore_WritableDefaultIsNotRelocated pins container behavior:
// where /var/lib IS writable (the image pre-seeds it), nothing changes and no
// existing checkpoint is stranded.
func TestCheckpointStore_WritableDefaultIsNotRelocated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LocalAppData", home)
	t.Setenv("XDG_STATE_HOME", "")

	// Stand in for a writable /var/lib by pointing the "default" at a writable
	// dir and telling checkpointStore that this is the default value.
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.FilePath = filepath.Join(dir, "checkpoints.json")

	_, out, err := checkpointStoreWithDefault(cfg, discardLogger(), cfg.Checkpoint.FilePath)
	if err != nil {
		t.Fatalf("checkpointStore: %v", err)
	}
	if out.Kind != "file" || out.Path != cfg.Checkpoint.FilePath {
		t.Errorf("kind=%q path=%q, want file at the configured (writable) default", out.Kind, out.Path)
	}
	if out.Reason != "" {
		t.Errorf("Reason = %q, want empty when nothing was relocated", out.Reason)
	}
}
