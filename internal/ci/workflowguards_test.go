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
		if condition, exists := job["if"]; exists {
			allowed := sentinel == "ci-success" && (name == "ac-evidence" || name == "privacy-sweep" || name == "backlog-freeze") && condition == "github.event_name == 'pull_request'"
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
	for _, tc := range []struct {
		name, file, gate, job string
		soft                  bool
	}{
		{"build condition", "ci.yml", "ci-success", "build-test", false},
		{"build soft failure", "ci.yml", "ci-success", "build-test", true},
		{"privacy condition", "ci.yml", "ci-success", "privacy-sweep", false},
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
