package ci_test

import (
	"fmt"
	"strings"
	"testing"
)

func requiredJob(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	jobs := jobsOf(t, doc, "workflow")
	job, ok := jobs[name].(map[string]any)
	if !ok {
		t.Fatalf("missing job %s", name)
	}
	return job
}

func requiredSteps(t *testing.T, job map[string]any) []any {
	t.Helper()
	steps, ok := job["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatal("missing steps")
	}
	return steps
}

// The fast path (rknightion/.github docs/changes/README.md). The changes job classifies a pull
// request; these application suites run only when it answers app: 'true', and fast-path-guards
// runs only when it answers app: 'false'. Every other job ci-success needs takes no changes
// condition, so lint, govulncheck, zizmor and the pull request checks always run.
var appSuites = map[string]bool{
	"build-test": true, "module-verify": true, "docs-catalog": true, "dashboards-drift": true,
	"goreleaser-snapshot": true, "docker-build": true,
}

const (
	appGate          = "needs.changes.outputs.app == 'true'"
	fastPathGate     = "needs.changes.outputs.app == 'false'"
	fastPathGuardJob = "fast-path-guards"
)

func needsJob(job map[string]any, name string) bool {
	switch needs := job["needs"].(type) {
	case string:
		return needs == name
	case []any:
		for _, need := range needs {
			if need == name {
				return true
			}
		}
	}
	return false
}

func gatingJobProblems(t *testing.T, doc map[string]any, sentinel string) []string {
	t.Helper()
	gate := requiredJob(t, doc, sentinel)
	needs, ok := gate["needs"].([]any)
	if !ok || len(needs) == 0 {
		t.Fatal("missing sentinel needs")
	}
	var problems []string
	for _, raw := range needs {
		name := raw.(string)
		job := requiredJob(t, doc, name)
		if name == "changes" {
			if _, exists := job["if"]; exists {
				problems = append(problems, "changes has job-level if")
			}
			continue
		}
		if sentinel == "ci-success" && name == "dependency-review" && job["if"] != "github.event_name == 'pull_request'" {
			problems = append(problems, name+" must be pull request only")
		}
		if condition, exists := job["if"]; exists {
			allowed := sentinel == "ci-success" && (name == "privacy-sweep" || name == "actionlint" || name == "dependency-review") && condition == "github.event_name == 'pull_request'"
			if sentinel == "ci-success" && needsJob(job, "changes") {
				allowed = allowed || (appSuites[name] && condition == appGate) || (name == fastPathGuardJob && condition == fastPathGate)
			}
			if !allowed {
				problems = append(problems, name+" has job-level if")
			}
		}
		if soft, exists := job["continue-on-error"]; exists && soft != false {
			problems = append(problems, name+" has job-level continue-on-error")
		}
		steps, _ := job["steps"].([]any)
		if _, reusable := job["uses"]; !reusable && len(steps) == 0 {
			t.Fatalf("%s has no steps", name)
		}
		for _, rawStep := range steps {
			step := rawStep.(map[string]any)
			if soft, exists := step["continue-on-error"]; exists && soft != false {
				problems = append(problems, name+" has step continue-on-error")
			}
		}
	}
	return problems
}

func TestGatingJobsCannotBeSkippedOrSoftFailed(t *testing.T) {
	for _, tc := range []struct{ file, gate string }{{"ci.yml", "ci-success"}, {"helm.yml", "helm-success"}} {
		if problems := gatingJobProblems(t, readWorkflow(t, tc.file), tc.gate); len(problems) > 0 {
			t.Errorf("%s: %v", tc.file, problems)
		}
	}
	t.Run("dependency review condition removed", func(t *testing.T) {
		doc := readWorkflow(t, "ci.yml")
		delete(requiredJob(t, doc, "dependency-review"), "if")
		if problems := gatingJobProblems(t, doc, "ci-success"); !strings.Contains(strings.Join(problems, "\n"), "dependency-review") {
			t.Fatalf("condition removal escaped guard: %v", problems)
		}
	})
	for _, tc := range []struct {
		name, file, job, condition string
		dropNeeds                  bool
	}{
		{"lint behind the app gate", "ci.yml", "lint", appGate, false},
		{"zizmor behind the app gate", "ci.yml", "zizmor", appGate, false},
		{"govulncheck behind the fast-path gate", "ci.yml", "govulncheck", fastPathGate, false},
		{"suite gate without needing changes", "ci.yml", "build-test", appGate, true},
		{"guards on the app gate", "ci.yml", fastPathGuardJob, appGate, false},
		{"changes with a condition", "ci.yml", "changes", "github.event_name == 'pull_request'", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := readWorkflow(t, tc.file)
			job := requiredJob(t, doc, tc.job)
			job["if"] = tc.condition
			if tc.dropNeeds {
				delete(job, "needs")
			}
			problems := gatingJobProblems(t, doc, "ci-success")
			if !strings.Contains(strings.Join(problems, "\n"), tc.job) {
				t.Fatalf("mutation escaped guard: %v", problems)
			}
		})
	}
	for _, tc := range []struct {
		name, file, gate, job string
		soft                  bool
	}{
		{"build condition", "ci.yml", "ci-success", "build-test", false},
		{"build soft failure", "ci.yml", "ci-success", "build-test", true},
		{"privacy condition", "ci.yml", "ci-success", "privacy-sweep", false},
		{"dependency review condition", "ci.yml", "ci-success", "dependency-review", false},
		{"actionlint condition", "ci.yml", "ci-success", "actionlint", false},
		{"helm condition", "helm.yml", "helm-success", "lint-template", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := readWorkflow(t, tc.file)
			job := requiredJob(t, doc, tc.job)
			if tc.soft {
				requiredSteps(t, job)[0].(map[string]any)["continue-on-error"] = true
			} else {
				job["if"] = "github.event_name == 'push'"
			}
			problems := gatingJobProblems(t, doc, tc.gate)
			if !strings.Contains(strings.Join(problems, "\n"), tc.job) {
				t.Fatalf("mutation escaped guard: %v", problems)
			}
		})
	}
}

// The backlog freeze is a step of ci-success, not a job of its own. It must run on every pull
// request even after an earlier step failed, and it must be able to read the file list.
func backlogFreezeProblems(t *testing.T, job map[string]any) []string {
	var problems []string
	perms, _ := job["permissions"].(map[string]any)
	if perms["pull-requests"] != "read" {
		problems = append(problems, "ci-success needs pull-requests: read")
	}
	found := 0
	for _, raw := range requiredSteps(t, job) {
		step := raw.(map[string]any)
		if step["name"] != "backlog/ is frozen" {
			continue
		}
		found++
		condition, _ := step["if"].(string)
		if !strings.Contains(condition, "always()") || !strings.Contains(condition, "github.event_name == 'pull_request'") {
			problems = append(problems, "backlog freeze step must run always() on pull requests")
		}
		if soft, exists := step["continue-on-error"]; exists && soft != false {
			problems = append(problems, "backlog freeze step has continue-on-error")
		}
		script, _ := step["run"].(string)
		if !strings.Contains(script, "startswith(\"backlog/\")") || !strings.Contains(script, "exit 1") {
			problems = append(problems, "backlog freeze step lost its check")
		}
	}
	if found != 1 {
		problems = append(problems, fmt.Sprintf("want one backlog freeze step, found %d", found))
	}
	return problems
}

func TestBacklogFreezeIsAGatedStepOfCISuccess(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	if _, ok := jobsOf(t, doc, "ci.yml")["backlog-freeze"]; ok {
		t.Fatal("backlog-freeze must stay a step of ci-success, not a separate job")
	}
	if problems := backlogFreezeProblems(t, requiredJob(t, doc, "ci-success")); len(problems) > 0 {
		t.Fatalf("%v", problems)
	}
	for name, mutate := range map[string]func(map[string]any, map[string]any){
		"condition dropped":  func(_, step map[string]any) { step["if"] = "github.event_name == 'pull_request'" },
		"soft failure":       func(_, step map[string]any) { step["continue-on-error"] = true },
		"permission dropped": func(job, _ map[string]any) { delete(job["permissions"].(map[string]any), "pull-requests") },
		"check removed":      func(_, step map[string]any) { step["run"] = "exit 0" },
	} {
		t.Run(name, func(t *testing.T) {
			job := requiredJob(t, readWorkflow(t, "ci.yml"), "ci-success")
			for _, raw := range requiredSteps(t, job) {
				if step := raw.(map[string]any); step["name"] == "backlog/ is frozen" {
					mutate(job, step)
				}
			}
			if len(backlogFreezeProblems(t, job)) == 0 {
				t.Fatal("mutation escaped guard")
			}
		})
	}
}

// workflowRecipeArguments returns actual invocations only, not comments or recipe bodies.
func workflowRecipeArguments(script, recipe string) []string {
	var arguments []string
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		prefix := "just " + recipe
		if line == prefix {
			arguments = append(arguments, "")
		} else if strings.HasPrefix(line, prefix+" ") {
			arguments = append(arguments, strings.TrimSpace(strings.TrimPrefix(line, prefix)))
		}
	}
	return arguments
}

func recipeArgumentProblems(script string, expected map[string]string) []string {
	var problems []string
	for recipe, want := range expected {
		args := workflowRecipeArguments(script, recipe)
		if len(args) != 1 || args[0] != want {
			problems = append(problems, fmt.Sprintf("%s arguments %q, want %q", recipe, args, want))
		}
	}
	return problems
}

func checkRecipeArguments(t *testing.T, steps []any, expected map[string]string) {
	t.Helper()
	script := workflowScript(steps)
	if problems := recipeArgumentProblems(script, expected); len(problems) > 0 {
		t.Errorf("%v", problems)
	}
	for recipe, args := range expected {
		t.Run("mutate "+recipe, func(t *testing.T) {
			replacement := "."
			if args == "." || args == "" {
				replacement = "SomeFilter"
			}
			old := "just " + recipe
			if args != "" {
				old += " " + args
			}
			mutated := strings.Replace(script, old, "just "+recipe+" "+replacement, 1)
			if mutated == script {
				t.Fatal("mutation did not change script")
			}
			problems := recipeArgumentProblems(mutated, expected)
			if !strings.Contains(strings.Join(problems, "\n"), recipe) {
				t.Fatal("argument mutation escaped guard")
			}
		})
	}
}

func hostedRunnerProblems(jobs map[string]any) []string {
	var problems []string
	for name, raw := range jobs {
		job, ok := raw.(map[string]any)
		if !ok {
			problems = append(problems, name+" missing job mapping")
			continue
		}
		label, ok := job["runs-on"].(string)
		if !ok || (!strings.HasPrefix(label, "ubuntu-") && !strings.HasPrefix(label, "windows-") && !strings.HasPrefix(label, "macos-")) {
			problems = append(problems, name+" requires a single hosted runs-on label")
		}
	}
	return problems
}
