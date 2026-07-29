package credreload

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNew_LoadsBearerTokenAndHeaderFiles(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	headerPath := filepath.Join(dir, "tenant")
	writeFile(t, tokenPath, "s3cr3t-token\n")
	writeFile(t, headerPath, "tenant-42")

	r, err := New(Options{Sources: Sources{
		BearerTokenFile: tokenPath,
		HeaderFiles:     map[string]string{"X-Scope-OrgID": headerPath},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Stop()

	headers := r.Headers()
	if got := headers["Authorization"]; got != "Bearer s3cr3t-token" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer s3cr3t-token")
	}
	if got := headers["X-Scope-OrgID"]; got != "tenant-42" {
		t.Errorf("X-Scope-OrgID header = %q, want %q", got, "tenant-42")
	}

	h := r.Health()
	if !h.Healthy {
		t.Errorf("Health().Healthy = false, want true after successful initial load")
	}
	if h.LastError != "" {
		t.Errorf("Health().LastError = %q, want empty", h.LastError)
	}
}

func TestNew_ZeroValueSourcesWatchesNothing(t *testing.T) {
	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Stop()

	if h := r.Headers(); len(h) != 0 {
		t.Errorf("Headers() = %v, want empty", h)
	}
	if tc := r.TLSConfig(); tc != nil {
		t.Errorf("TLSConfig() = %+v, want nil", tc)
	}
	if !r.Health().Healthy {
		t.Errorf("Health().Healthy = false, want true (nothing to load is trivially successful)")
	}
}

func TestNew_FailsFastOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := New(Options{Sources: Sources{
		BearerTokenFile: filepath.Join(dir, "does-not-exist"),
	}})
	if err == nil {
		t.Fatal("New: want error for missing token file, got nil")
	}
}

func TestNew_FailsFastOnEmptyTokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	writeFile(t, path, "   \n")

	_, err := New(Options{Sources: Sources{BearerTokenFile: path}})
	if err == nil {
		t.Fatal("New: want error for empty (whitespace-only) token file, got nil")
	}
}

func TestReload_MalformedReplacementRetainsLastKnownGood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	writeFile(t, path, "good-token")

	r, err := New(Options{Sources: Sources{BearerTokenFile: path}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Stop()

	if got := r.Headers()["Authorization"]; got != "Bearer good-token" {
		t.Fatalf("initial Authorization = %q", got)
	}

	// Replace with an empty file - a malformed replacement.
	writeFile(t, path, "")

	if err := r.Reload(); err == nil {
		t.Fatal("Reload: want error for empty replacement token, got nil")
	}

	// The exporter-facing accessor must still serve the old, good value.
	if got := r.Headers()["Authorization"]; got != "Bearer good-token" {
		t.Errorf("Authorization after bad reload = %q, want unchanged %q", got, "Bearer good-token")
	}

	h := r.Health()
	if h.LastError == "" {
		t.Error("Health().LastError = empty, want a reason after a failed reload")
	}
	if h.ConsecutiveFailures != 1 {
		t.Errorf("Health().ConsecutiveFailures = %d, want 1", h.ConsecutiveFailures)
	}
	if !h.Healthy {
		t.Error("Health().Healthy = false, want true: the served snapshot is still the good one")
	}
}

func TestReload_PicksUpValidRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	writeFile(t, path, "token-v1")

	r, err := New(Options{Sources: Sources{BearerTokenFile: path}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Stop()

	// Ensure a distinct mtime from the write in New(), some filesystems have
	// coarse mtime resolution.
	future := time.Now().Add(time.Second)
	writeFile(t, path, "token-v2")
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := r.Headers()["Authorization"]; got != "Bearer token-v2" {
		t.Errorf("Authorization after rotation = %q, want %q", got, "Bearer token-v2")
	}
	if h := r.Health(); h.ConsecutiveFailures != 0 || !h.Healthy {
		t.Errorf("Health after good rotation = %+v", h)
	}
}

func TestReload_RecoverAfterBadThenGood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	writeFile(t, path, "token-v1")

	r, err := New(Options{Sources: Sources{BearerTokenFile: path}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Stop()

	bad := time.Now().Add(time.Second)
	writeFile(t, path, "")
	if err := os.Chtimes(path, bad, bad); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := r.Reload(); err == nil {
		t.Fatal("Reload: want error for empty file")
	}

	good := bad.Add(time.Second)
	writeFile(t, path, "token-v3")
	if err := os.Chtimes(path, good, good); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload after fix: %v", err)
	}

	if got := r.Headers()["Authorization"]; got != "Bearer token-v3" {
		t.Errorf("Authorization after recovery = %q, want %q", got, "Bearer token-v3")
	}
	if h := r.Health(); h.ConsecutiveFailures != 0 || h.LastError != "" {
		t.Errorf("Health after recovery = %+v, want clean", h)
	}
}
