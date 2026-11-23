package ci_test

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The five lint legs and four tool-module verify legs are packed into two jobs
// (#1145). A matrix made a dropped leg visible as a missing check; packed steps
// do not, so these guards carry that coverage: every module root, every command,
// each leg running after another leg's failure, and each tool module verified in
// its own scratch worktree, because a failed go mod tidy rewrites go.mod in place.
const (
	packedLintIf    = "${{ !cancelled() && steps.pins.outcome == 'success' }}"    //nolint:misspell // GitHub Actions names this status function cancelled().
	packedVerifyIf  = "${{ !cancelled() && steps.scratch.outcome == 'success' }}" //nolint:misspell // GitHub Actions names this status function cancelled().
	packedLintPin   = "${{ steps.pins.outputs.golangci }}"
	packedScratchWD = "${{ runner.temp }}/verify/"
)

var packedVerifyRecipes = []string{"test-modules", "vet", "vuln", "tidy-check"}

func toolModulesOf(modules []string) []string {
	var tools []string
	for _, module := range modules {
		if module != "." {
			tools = append(tools, module)
		}
	}
	return tools
}

func packedVerifyScript(module string) string {
	var b strings.Builder
	b.WriteString("status=0\n")
	for _, recipe := range packedVerifyRecipes {
		fmt.Fprintf(&b, "just %s %s || status=1\n", recipe, module)
	}
	b.WriteString("exit \"$status\"\n")
	return b.String()
}

func packedSteps(job map[string]any) []map[string]any {
	raw, _ := job["steps"].([]any)
	steps := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if step, ok := entry.(map[string]any); ok {
			steps = append(steps, step)
		}
	}
	return steps
}

func packedLintProblems(job map[string]any, modules []string) []string {
	var problems []string
	if _, exists := job["strategy"]; exists {
		problems = append(problems, "lint must stay one packed job, not a matrix")
	}
	seen := map[string]int{}
	for _, step := range packedSteps(job) {
		uses, _ := step["uses"].(string)
		if !strings.HasPrefix(uses, "golangci/golangci-lint-action@") {
			continue
		}
		with, _ := step["with"].(map[string]any)
		module, _ := with["working-directory"].(string)
		seen[module]++
		if step["if"] != packedLintIf {
			problems = append(problems, fmt.Sprintf("lint step for %q has if %v, want %s", module, step["if"], packedLintIf))
		}
		if with["version"] != packedLintPin {
			problems = append(problems, fmt.Sprintf("lint step for %q has version %v, want the justfile pin", module, with["version"]))
		}
		// One step owns the shared cache: five saves would race on one directory.
		if skip := with["skip-cache"] == true; skip == (module == ".") {
			problems = append(problems, fmt.Sprintf("lint step for %q has skip-cache %v; only the root step owns the cache", module, with["skip-cache"]))
		}
		if !slices.Contains(modules, module) {
			problems = append(problems, fmt.Sprintf("lint step names unknown module %q", module))
		}
	}
	for _, module := range modules {
		if seen[module] != 1 {
			problems = append(problems, fmt.Sprintf("module %q has %d lint steps, want 1", module, seen[module]))
		}
	}
	return problems
}

// packedVerifyStep returns the verify step whose test-modules recipe names module.
func packedVerifyStep(job map[string]any, module string) map[string]any {
	for _, step := range packedSteps(job) {
		run, _ := step["run"].(string)
		if slices.Contains(workflowRecipeArguments(run, "test-modules"), module+" || status=1") {
			return step
		}
	}
	return nil
}

func packedVerifyProblems(job map[string]any, tools []string) []string {
	var problems []string
	if _, exists := job["strategy"]; exists {
		problems = append(problems, "module-verify must stay one packed job, not a matrix")
	}
	seen := map[string]int{}
	for _, step := range packedSteps(job) {
		directory, _ := step["working-directory"].(string)
		if !strings.HasPrefix(directory, packedScratchWD) {
			continue
		}
		base := strings.TrimPrefix(directory, packedScratchWD)
		seen[base]++
		module := "tools/" + base
		if !slices.Contains(tools, module) {
			problems = append(problems, fmt.Sprintf("verify step runs in unknown scratch tree %q", base))
		}
		if step["if"] != packedVerifyIf {
			problems = append(problems, fmt.Sprintf("verify step for %s has if %v, want %s", module, step["if"], packedVerifyIf))
		}
		if run, _ := step["run"].(string); run != packedVerifyScript(module) {
			problems = append(problems, fmt.Sprintf("verify step for %s must run every command, tidy last, and report any failure; got %q", module, run))
		}
	}
	for _, module := range tools {
		if seen[path.Base(module)] != 1 {
			problems = append(problems, fmt.Sprintf("module %q has %d verify steps in its own scratch tree, want 1", module, seen[path.Base(module)]))
		}
	}
	return problems
}

func packedJobs(t *testing.T) (lint, verify map[string]any) {
	t.Helper()
	doc := readWorkflow(t, "ci.yml")
	return requiredJob(t, doc, "lint"), requiredJob(t, doc, "module-verify")
}

func dropPackedStep(job, target map[string]any) {
	var kept []any
	for _, raw := range job["steps"].([]any) {
		if step, ok := raw.(map[string]any); !ok || fmt.Sprint(step) != fmt.Sprint(target) {
			kept = append(kept, raw)
		}
	}
	job["steps"] = kept
}

func TestPackedLegsStayRequired(t *testing.T) {
	gate := requiredJob(t, readWorkflow(t, "ci.yml"), "ci-success")
	needs, _ := gate["needs"].([]any)
	for _, job := range []string{"lint", "module-verify"} {
		if !slices.Contains(needs, any(job)) {
			t.Errorf("ci-success does not require %s", job)
		}
	}
}

func TestPackedLintGuardDetectsDrift(t *testing.T) {
	modules := goModules(t)
	if lint, _ := packedJobs(t); len(packedLintProblems(lint, modules)) != 0 {
		t.Fatalf("baseline: %v", packedLintProblems(lint, modules))
	}
	lintStep := func(job map[string]any, module string) map[string]any {
		for _, step := range packedSteps(job) {
			if with, ok := step["with"].(map[string]any); ok && with["working-directory"] == module {
				return step
			}
		}
		t.Fatalf("no lint step for %s", module)
		return nil
	}
	for _, module := range modules {
		for name, mutate := range map[string]func(map[string]any){
			"dropped":             func(job map[string]any) { dropPackedStep(job, lintStep(job, module)) },
			"stops after failure": func(job map[string]any) { delete(lintStep(job, module), "if") },
			"literal version": func(job map[string]any) {
				lintStep(job, module)["with"].(map[string]any)["version"] = "v2.15.0"
			},
			"cache owner moved": func(job map[string]any) {
				with := lintStep(job, module)["with"].(map[string]any)
				if module == "." {
					with["skip-cache"] = true
				} else {
					delete(with, "skip-cache")
				}
			},
		} {
			t.Run(module+"/"+name, func(t *testing.T) {
				job, _ := packedJobs(t)
				mutate(job)
				if len(packedLintProblems(job, modules)) == 0 {
					t.Fatal("mutation escaped guard")
				}
			})
		}
	}
}

func TestPackedVerifyGuardDetectsDrift(t *testing.T) {
	tools := toolModulesOf(goModules(t))
	if _, verify := packedJobs(t); len(packedVerifyProblems(verify, tools)) != 0 {
		t.Fatalf("baseline: %v", packedVerifyProblems(verify, tools))
	}
	for _, module := range tools {
		mutations := map[string]func(map[string]any){
			"dropped":             func(job map[string]any) { dropPackedStep(job, packedVerifyStep(job, module)) },
			"stops after failure": func(job map[string]any) { delete(packedVerifyStep(job, module), "if") },
			"shared tree": func(job map[string]any) {
				packedVerifyStep(job, module)["working-directory"] = "."
			},
			"failure swallowed": func(job map[string]any) {
				step := packedVerifyStep(job, module)
				step["run"] = strings.Replace(step["run"].(string), "exit \"$status\"", "exit 0", 1)
			},
			"tidy before vuln": func(job map[string]any) {
				step := packedVerifyStep(job, module)
				run := step["run"].(string)
				vuln, tidy := "just vuln "+module, "just tidy-check "+module
				run = strings.ReplaceAll(run, vuln, "SWAP")
				run = strings.ReplaceAll(run, tidy, vuln)
				step["run"] = strings.ReplaceAll(run, "SWAP", tidy)
			},
		}
		for _, recipe := range packedVerifyRecipes {
			mutations["drops "+recipe] = func(job map[string]any) {
				step := packedVerifyStep(job, module)
				step["run"] = strings.Replace(step["run"].(string), fmt.Sprintf("just %s %s || status=1\n", recipe, module), "", 1)
			}
		}
		for name, mutate := range mutations {
			t.Run(module+"/"+name, func(t *testing.T) {
				_, job := packedJobs(t)
				mutate(job)
				if len(packedVerifyProblems(job, tools)) == 0 {
					t.Fatal("mutation escaped guard")
				}
			})
		}
	}
}

// Runs each real verify script against a fake just: a failure in any one command
// must fail the step and must not stop the commands after it.
func TestPackedVerifyScriptRunsEveryCommandAndReportsFailure(t *testing.T) {
	_, job := packedJobs(t)
	for _, module := range toolModulesOf(goModules(t)) {
		step := packedVerifyStep(job, module)
		if step == nil {
			t.Fatalf("no verify step for %s", module)
		}
		script := step["run"].(string)
		for _, failing := range append([]string{""}, packedVerifyRecipes...) {
			t.Run(module+"/fail="+failing, func(t *testing.T) {
				dir := t.TempDir()
				calls := filepath.Join(dir, "calls")
				fake := fmt.Sprintf("#!/bin/sh\necho \"$1 $2\" >> %q\n[ \"$1\" != %q ]\n", calls, failing)
				if err := os.WriteFile(filepath.Join(dir, "just"), []byte(fake), 0o755); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("bash", "--noprofile", "--norc", "-e", "-c", script)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
				err := cmd.Run()
				if (err != nil) != (failing != "") {
					t.Fatalf("failing=%q: exit error %v", failing, err)
				}
				got, readErr := os.ReadFile(calls)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var want strings.Builder
				for _, recipe := range packedVerifyRecipes {
					fmt.Fprintf(&want, "%s %s\n", recipe, module)
				}
				if string(got) != want.String() {
					t.Fatalf("calls = %q, want %q", got, want.String())
				}
			})
		}
	}
}

// packedScratchProblems runs the real scratch step in a throwaway repository and
// proves a tidy-style rewrite in one module's tree reaches neither the checkout
// nor another module's tree.
func packedScratchProblems(t *testing.T, script string, tools []string) []string {
	t.Helper()
	const seed = "module scratch\n"
	const rewritten = "rewritten by a failed tidy\n"
	repo, temp := t.TempDir(), t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=ci", "-c", "user.email=ci@example.com"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "go.mod")
	git("commit", "-q", "-m", "seed")
	cmd := exec.Command("bash", "--noprofile", "--norc", "-e", "-c", script)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+temp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return []string{fmt.Sprintf("scratch step failed: %v\n%s", err, out)}
	}
	treeOf := func(module string) string { return filepath.Join(temp, "verify", path.Base(module), "go.mod") }
	var problems []string
	for i, module := range tools {
		got, err := os.ReadFile(treeOf(module))
		if err != nil || string(got) != seed {
			problems = append(problems, fmt.Sprintf("%s has no clean scratch tree (%v)", module, err))
			continue
		}
		if err := os.WriteFile(treeOf(module), []byte(rewritten), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(filepath.Join(repo, "go.mod")); string(got) != seed {
			problems = append(problems, module+" scratch tree is not isolated from the checkout")
		}
		for _, other := range tools[i+1:] {
			if got, err := os.ReadFile(treeOf(other)); err == nil && string(got) == rewritten {
				problems = append(problems, module+" shares a scratch tree with "+other)
			}
		}
	}
	return problems
}

func TestPackedVerifyScratchTreesAreIsolated(t *testing.T) {
	_, job := packedJobs(t)
	tools := toolModulesOf(goModules(t))
	var script string
	for _, step := range packedSteps(job) {
		if step["id"] == "scratch" {
			script, _ = step["run"].(string)
		}
	}
	if script == "" {
		t.Fatal("module-verify has no scratch step")
	}
	if problems := packedScratchProblems(t, script, tools); len(problems) != 0 {
		t.Fatalf("%v", problems)
	}
	for _, module := range tools {
		t.Run("drops "+module, func(t *testing.T) {
			mutated := strings.Replace(script, " "+path.Base(module), "", 1)
			if mutated == script {
				t.Fatal("mutation did not change script")
			}
			if len(packedScratchProblems(t, mutated, tools)) == 0 {
				t.Fatal("mutation escaped guard")
			}
		})
	}
}
