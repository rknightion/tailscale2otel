package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// kubeletLayout builds what the kubelet's AtomicWriter leaves in a
// ConfigMap/Secret volume: a timestamped "..<ts>" directory holding the real
// file, a "..data" symlink to it, and a top-level symlink per key.
func kubeletLayout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ts := filepath.Join(dir, "..2026_10_09_12_00_00.000000001")
	if err := os.Mkdir(ts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ts, "config.yaml"), []byte("tailscale:\n  tailnet: example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(ts), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "config.yaml"), filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "config.yaml")
}

func stubReadOnlyMount(t *testing.T, ro bool) {
	t.Helper()
	orig := readOnlyMount
	readOnlyMount = func(string) (bool, error) { return ro, nil }
	t.Cleanup(func() { readOnlyMount = orig })
}

func hasPermWarning(ws []string) bool {
	return slices.ContainsFunc(ws, func(w string) bool { return strings.Contains(w, "readable by group/other") })
}

// A kubelet-projected config on a read-only mount cannot be chmod'ed (the
// chart's 0644 is the only mode the non-root uid can read), so the advisory
// would stay red forever with no remedy. It is suppressed there and nowhere else.
func TestLoadSkipsPermWarningForKubeletProjectedReadOnlyConfig(t *testing.T) {
	path := kubeletLayout(t)

	stubReadOnlyMount(t, true)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if hasPermWarning(cfg.Warnings()) {
		t.Errorf("kubelet-projected read-only config must not warn, got %q", cfg.Warnings())
	}

	// The same layout on a WRITABLE mount is not a kubelet volume (or is one an
	// operator can fix), so it still warns.
	stubReadOnlyMount(t, false)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !hasPermWarning(cfg.Warnings()) {
		t.Errorf("..data layout on a writable mount must still warn, got %q", cfg.Warnings())
	}
}

// An ordinary 0644 file must still warn even when its mount is read-only:
// only the AtomicWriter layout is exempt.
func TestLoadPermWarningPlainFileOnReadOnlyMount(t *testing.T) {
	stubReadOnlyMount(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("tailscale:\n  tailnet: example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !hasPermWarning(cfg.Warnings()) {
		t.Errorf("plain 0644 file must warn, got %q", cfg.Warnings())
	}

	// A plain symlink to a file outside any ..data directory is not the
	// kubelet layout either.
	link := filepath.Join(dir, "linked.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	if kubeletProjectedReadOnly(link) {
		t.Error("..data pointing at a non-'..<timestamp>' directory must not count as the kubelet layout")
	}
}

// The real platform probe must report a writable temp directory as writable;
// an inverted flag test would otherwise silently exempt every config file.
func TestReadOnlyMountRealProbeOnWritableDir(t *testing.T) {
	ro, err := readOnlyMount(t.TempDir())
	if err != nil {
		t.Fatalf("readOnlyMount: %v", err)
	}
	if ro {
		t.Error("readOnlyMount reported a writable temp dir as read-only")
	}
}
