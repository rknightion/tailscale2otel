package repotest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/repotest"
)

func TestRootFindsTheModuleRoot(t *testing.T) {
	root, err := repotest.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("Root = %q, want an absolute path", root)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("Root = %q has no go.mod: %v", root, err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "repotest")); err != nil {
		t.Fatalf("Root = %q is not this repository root: %v", root, err)
	}
}
