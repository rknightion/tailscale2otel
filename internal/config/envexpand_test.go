package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/config"
)

// TestLoadWarnsOnUndefinedEnvVar: a ${VAR} reference that is undefined at load
// time expands to "" silently. That is the root cause of a typo'd credential var
// producing an unauthenticated receiver, so Load must surface it as a Warning.
func TestLoadWarnsOnUndefinedEnvVar(t *testing.T) {
	const varName = "T2O_UNSET_CANARY_VAR"
	os.Unsetenv(varName)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	doc := "self_observability:\n  instance_id: \"${" + varName + "}\"\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var found bool
	for _, w := range cfg.Warnings() {
		if strings.Contains(w, varName) {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings() = %v, want one naming the undefined env var %q", cfg.Warnings(), varName)
	}
}
