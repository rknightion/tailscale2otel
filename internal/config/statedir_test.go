package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDefaultCheckpointPath_PlatformAppropriate pins the per-platform state
// directory. The old default, /var/lib/tailscale2otel, is writable only by root
// on Linux and does not exist at all on macOS or Windows — and releases ship
// binaries for all three — so a native run silently fell back to in-memory
// checkpoints and cold-started from initial_lookback on every restart (#336).
func TestDefaultCheckpointPath_PlatformAppropriate(t *testing.T) {
	home := t.TempDir()

	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", home)
	case "darwin":
		t.Setenv("HOME", home)
	default:
		t.Setenv("HOME", home)
		t.Setenv("XDG_STATE_HOME", "")
	}

	got := DefaultCheckpointPath()
	if got == LegacyCheckpointPath {
		t.Fatalf("DefaultCheckpointPath() returned the legacy root-only path %q; "+
			"a non-root or non-Linux run cannot write there", got)
	}
	if !strings.HasPrefix(got, home) {
		t.Errorf("DefaultCheckpointPath() = %q, want a path under the user's home %q", got, home)
	}
	if filepath.Base(got) != "checkpoints.json" {
		t.Errorf("DefaultCheckpointPath() = %q, want it to end in checkpoints.json", got)
	}
	if !strings.Contains(got, "tailscale2otel") {
		t.Errorf("DefaultCheckpointPath() = %q, want it namespaced under tailscale2otel", got)
	}

	// The per-platform convention itself, so a change of directory is a
	// deliberate edit rather than an accident.
	var want string
	switch runtime.GOOS {
	case "windows":
		want = filepath.Join(home, "tailscale2otel", "checkpoints.json")
	case "darwin":
		want = filepath.Join(home, "Library", "Application Support", "tailscale2otel", "checkpoints.json")
	default:
		want = filepath.Join(home, ".local", "state", "tailscale2otel", "checkpoints.json")
	}
	if got != want {
		t.Errorf("DefaultCheckpointPath() = %q, want %q for GOOS=%s", got, want, runtime.GOOS)
	}
}

// TestDefaultCheckpointPath_XDGStateHomeWins pins XDG_STATE_HOME on the
// platforms that honor it. Operators who relocate state expect it respected.
func TestDefaultCheckpointPath_XDGStateHomeWins(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skipf("XDG_STATE_HOME is not the convention on %s", runtime.GOOS)
	}
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", dir)
	want := filepath.Join(dir, "tailscale2otel", "checkpoints.json")
	if got := DefaultCheckpointPath(); got != want {
		t.Errorf("DefaultCheckpointPath() = %q, want %q", got, want)
	}
}

// TestDefaultCheckpointPath_NoHomeFallsBackToLegacy pins the last resort. With
// no resolvable home there is nowhere better than the historical path, and the
// existing unwritable-path WARN then handles it gracefully — a panic or an
// empty path (which means "memory store") would both be worse.
func TestDefaultCheckpointPath_NoHomeFallsBackToLegacy(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("LocalAppData", "")
	t.Setenv("USERPROFILE", "")
	if got := DefaultCheckpointPath(); got != LegacyCheckpointPath {
		t.Errorf("DefaultCheckpointPath() with no home = %q, want the legacy path %q",
			got, LegacyCheckpointPath)
	}
}

// TestDefaultKeepsLegacyCheckpointPath pins a deliberate NON-change.
//
// Default() must stay the legacy container path, and the platform path is
// applied at runtime instead (see internal/app.checkpointStore). Two invariants
// force this, and a future reader is likely to try "fixing" it:
//
//  1. TestExampleConfigMatchesDefaults gates config.example.yaml to match
//     Default() exactly. A machine-dependent default cannot be written into a
//     committed file, so that gate would have to be weakened to accommodate it.
//  2. The container image and Helm chart both depend on /var/lib being the
//     default. Changing it would silently move every container's checkpoint
//     unless every image and compose file also set it explicitly.
//
// Resolving at runtime keeps the committed default deterministic AND leaves
// container behavior untouched, because /var/lib IS writable there.
func TestDefaultKeepsLegacyCheckpointPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := Default().Checkpoint.FilePath; got != LegacyCheckpointPath {
		t.Errorf("Default().Checkpoint.FilePath = %q, want the deterministic legacy path %q "+
			"(the platform path is applied at runtime, not baked into the default)",
			got, LegacyCheckpointPath)
	}
}
