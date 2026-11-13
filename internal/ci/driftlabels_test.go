package ci_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestEveryLaneLabelIsDeclaredAndDocumented(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github/actions/report-drift/README.md"))
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, file := range workflowFiles(t) {
		for _, rawJob := range jobsOf(t, readWorkflow(t, file), file) {
			job := rawJob.(map[string]any)
			steps, _ := job["steps"].([]any)
			for _, rawStep := range steps {
				step := rawStep.(map[string]any)
				with, _ := step["with"].(map[string]any)
				if label, ok := with["lane-label"].(string); ok {
					used[label] = true
				}
			}
		}
	}
	for label := range used {
		if !driftLaneLabels[label] {
			t.Errorf("undeclared lane label %s", label)
		}
		if !strings.Contains(string(raw), "gh label create "+label+" ") {
			t.Errorf("undocumented label setup %s", label)
		}
	}
	for label := range driftLaneLabels {
		if !used[label] {
			t.Errorf("unused lane label %s", label)
		}
	}
}

func TestScheduledFuzzLaneResolvesOnACleanRun(t *testing.T) {
	job := requiredJob(t, readWorkflow(t, "fuzz-scheduled.yml"), "report")
	found := 0
	for _, raw := range requiredSteps(t, job) {
		step := raw.(map[string]any)
		if step["uses"] != "./.github/actions/resolve-drift" {
			continue
		}
		found++
		with, _ := step["with"].(map[string]any)
		if with["lane-label"] != "fuzz-finding" || step["if"] != "steps.assemble.outputs.findings == '0'" {
			t.Errorf("incorrect resolve step: %v", step)
		}
	}
	if found != 1 {
		t.Fatalf("got %d resolve steps, want one", found)
	}
}

func TestReportDriftRejectsMissingLabelBeforeWriting(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github/actions/report-drift/action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	runs := doc["runs"].(map[string]any)
	steps := runs["steps"].([]any)
	script := ""
	for _, raw := range steps {
		step := raw.(map[string]any)
		if step["name"] == "Upsert tracking issue" {
			script, _ = step["run"].(string)
		}
	}
	if script == "" {
		t.Fatal("missing upsert script")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "calls")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TEST_GH_CALLS\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_GH_CALLS="+marker, "GITHUB_REPOSITORY=example/repo", "INPUTS_LANE_LABEL=example-label", "INPUTS_TITLE=example", "INPUTS_REPORT_PATH=example.md")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("missing label accepted")
	}
	if !strings.Contains(string(out), "::error title=report-drift::label example-label does not exist") {
		t.Errorf("missing actionable annotation: %s", out)
	}
	calls, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "api repos/example/repo/labels/example-label --silent\n" {
		t.Errorf("unexpected calls: %s", calls)
	}
}
