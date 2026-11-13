package ci_test

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func zizmorGateError(doc map[string]any) error {
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing jobs")
	}
	job, ok := jobs["zizmor"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing zizmor job")
	}
	with, _ := job["with"].(map[string]any)
	if with["enforce"] != true {
		return fmt.Errorf("zizmor must enforce")
	}
	if _, exists := job["if"]; exists {
		return fmt.Errorf("zizmor must not be conditional")
	}
	gate, ok := jobs["ci-success"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing ci-success")
	}
	needs, _ := gate["needs"].([]any)
	for _, need := range needs {
		if need == "zizmor" {
			return nil
		}
	}
	return fmt.Errorf("ci-success must require zizmor")
}

func zizmorPolicyError(paths []string) error {
	for _, path := range paths {
		if path == ".github/zizmor.yml" || path == "zizmor.yml" {
			return fmt.Errorf("shared workflow owns zizmor policy; repository copy %s replaces it", path)
		}
	}
	return nil
}

var zizmorSuppressions = map[string]int{
	".github/workflows/ci.yml:cache-poisoning":             2,
	".github/workflows/fuzz-scheduled.yml:cache-poisoning": 1,
	".github/workflows/auto-rc.yml:dangerous-triggers":     1,
	".github/workflows/grafana-sync.yml:artipacked":        1,
}

func zizmorSuppressionError(files map[string]string) error {
	found := map[string]int{}
	pattern := regexp.MustCompile(`zizmor:\s*ignore\[([^\]]+)\]`)
	for path, content := range files {
		for _, match := range pattern.FindAllStringSubmatch(content, -1) {
			for _, rule := range strings.Split(match[1], ",") {
				found[path+":"+strings.TrimSpace(rule)]++
			}
		}
	}
	if !maps.Equal(found, zizmorSuppressions) {
		return fmt.Errorf("zizmor suppressions %v, want explicit allowlist %v", found, zizmorSuppressions)
	}
	return nil
}

func TestZizmorGateIsEnforcing(t *testing.T) {
	if err := zizmorGateError(readWorkflow(t, "ci.yml")); err != nil {
		t.Fatal(err)
	}
}

func TestRepositorySuppliesNoZizmorPolicy(t *testing.T) {
	var existing []string
	for _, path := range []string{".github/zizmor.yml", "zizmor.yml"} {
		_, err := os.Stat(filepath.Join(repositoryRoot(t), path))
		if err == nil {
			existing = append(existing, path)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := zizmorPolicyError(existing); err != nil {
		t.Fatal(err)
	}
}

func suppressionFiles(t *testing.T) map[string]string {
	t.Helper()
	root := repositoryRoot(t)
	files := map[string]string{}
	for _, dir := range []string{".github/workflows", ".github/actions"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(relative)] = string(raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no workflow or action files")
	}
	return files
}

func TestZizmorSuppressionsAreAnExplicitAllowlist(t *testing.T) {
	if err := zizmorSuppressionError(suppressionFiles(t)); err != nil {
		t.Fatal(err)
	}
}

func TestZizmorChecksRejectViolations(t *testing.T) {
	for _, mutation := range []string{"missing job", "enforce", "condition", "needs"} {
		t.Run(mutation, func(t *testing.T) {
			doc := readWorkflow(t, "ci.yml")
			jobs := doc["jobs"].(map[string]any)
			job := jobs["zizmor"].(map[string]any)
			switch mutation {
			case "missing job":
				delete(jobs, "zizmor")
			case "enforce":
				delete(job["with"].(map[string]any), "enforce")
			case "condition":
				job["if"] = "false"
			case "needs":
				gate := jobs["ci-success"].(map[string]any)
				needs := gate["needs"].([]any)
				var filtered []any
				for _, need := range needs {
					if need != "zizmor" {
						filtered = append(filtered, need)
					}
				}
				gate["needs"] = filtered
			}
			if zizmorGateError(doc) == nil {
				t.Fatal("gate mutation escaped check")
			}
		})
	}
	for _, path := range []string{".github/zizmor.yml", "zizmor.yml"} {
		t.Run(path, func(t *testing.T) {
			if zizmorPolicyError([]string{path}) == nil {
				t.Fatal("policy mutation escaped check")
			}
		})
	}
	for _, mutation := range []string{"added workflow", "added action", "removed"} {
		t.Run(mutation, func(t *testing.T) {
			files := suppressionFiles(t)
			switch mutation {
			case "added workflow":
				files[".github/workflows/example.yml"] = "# zizmor: ignore[artipacked]"
			case "added action":
				files[".github/actions/example/action.yml"] = "# zizmor: ignore[artipacked]"
			case "removed":
				delete(files, ".github/workflows/auto-rc.yml")
			}
			if zizmorSuppressionError(files) == nil {
				t.Fatal("suppression mutation escaped check")
			}
		})
	}
}
