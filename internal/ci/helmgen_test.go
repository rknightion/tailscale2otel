package ci_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelmGenCheckRequiresPinnedGenerators(t *testing.T) {
	root := repositoryRoot(t)
	justPath, err := exec.LookPath("just")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	marker := filepath.Join(shimDir, "invoked")
	for _, name := range []string{"helm-docs", "helm-values-schema-json"} {
		script := "#!/bin/sh\nif [ \"$1\" = --version ]; then\necho 'version v0.0.1'\nexit 0\nfi\necho invoked >> \"$TOOLPIN_MARKER\"\n"
		if err := os.WriteFile(filepath.Join(shimDir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	chartFiles := []string{"deploy/helm/tailscale2otel/README.md", "deploy/helm/tailscale2otel/values.schema.json"}
	status := func() string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"status", "--porcelain", "--"}, chartFiles...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("status: %s: %v", out, err)
		}
		return string(out)
	}
	before := status()
	originals := make(map[string]string)
	for _, name := range chartFiles {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		originals[name] = string(raw)
	}
	for _, recipe := range []string{"helm-gen-check", "gen-helm"} {
		cmd := exec.Command(justPath, recipe)
		cmd.Dir = root
		cmd.Env = toolTestEnv(shimDir, marker, "")
		// The lenient recipe must be tested independently of the caller's strict setting.
		cmd.Env = append(cmd.Env, "REGEN_REQUIRE_TOOLS=0")
		out, err := cmd.CombinedOutput()
		if recipe == "helm-gen-check" {
			if err == nil {
				t.Errorf("strict check accepted wrong generators: %s", out)
			}
			if !strings.Contains(string(out), "just gen-tools") {
				t.Errorf("missing repair command: %s", out)
			}
		} else if err != nil || !strings.Contains(string(out), "SKIP") {
			t.Errorf("lenient generation: %s: %v", out, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("generator invoked, marker stat: %v", err)
		}
		if after := status(); after != before {
			t.Errorf("%s changed chart status: %q -> %q", recipe, before, after)
		}
		for _, name := range chartFiles {
			raw, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != originals[name] {
				t.Errorf("%s changed %s", recipe, name)
			}
		}
	}
}
