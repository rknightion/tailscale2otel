package ci_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestJustfileScriptsAreExecutable(t *testing.T) {
	count := 0
	for name, recipe := range justRecipes(t) {
		for _, line := range recipe.body {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			path := fields[0]
			if !strings.HasPrefix(path, "scripts/") && !strings.HasPrefix(path, "deploy/") {
				continue
			}
			count++
			info, err := os.Stat(filepath.Join(repositoryRoot(t), path))
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if info.Mode().Perm()&0o111 == 0 {
				t.Errorf("%s directly executes non-executable %s", name, path)
			}
		}
	}
	if count < 5 {
		t.Fatalf("only inspected %d direct script invocations", count)
	}
}

func TestBumpModuleMajorPreservesFileModes(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(repositoryRoot(t), "scripts/bump-module-major.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, sub := range []string{"scripts", "shim", "tools/example"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const module = "github.com/rknightion/tailscale2otel/v5"
	files := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"go.mod":                       {"module " + module + "\n", 0o644},
		"scripts/bump-module-major.sh": {string(script), 0o755},
		"executable.sh":                {"#!/bin/sh\n# " + module + "\n", 0o755},
		"plain.txt":                    {module + "\n", 0o644},
		"tools/example/go.mod":         {"module example\nrequire " + module + " v5.2.1\n", 0o644},
		"shim/go":                      {"#!/bin/sh\nexit 0\n", 0o755},
	}
	for name, file := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(file.content), file.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, name), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "go.mod", "scripts", "executable.sh", "plain.txt", "tools"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s: %v", out, err)
		}
	}
	cmd := exec.Command("bash", "scripts/bump-module-major.sh", "6")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "shim")+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bump: %s: %v", out, err)
	}
	for _, name := range []string{"executable.sh", "plain.txt", "tools/example/go.mod"} {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "tailscale2otel/v6") || strings.Contains(string(content), "tailscale2otel/v5") {
			t.Errorf("%s was not rewritten: %s", name, content)
		}
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != files[name].mode {
			t.Errorf("%s mode = %o, want %o", name, info.Mode().Perm(), files[name].mode)
		}
	}
	content, err := os.ReadFile(filepath.Join(dir, "tools/example/go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "v6.0.0") {
		t.Errorf("tool requirement version not rewritten: %s", content)
	}
}
