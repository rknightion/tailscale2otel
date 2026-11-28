package ci_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const verdictStepName = "Fail if an application suite was skipped without a verdict"

var verdictExpression = regexp.MustCompile(`\$\{\{\s*([^}]*?)\s*\}\}`)

// renderVerdictEnv expands the verdict step's env from synthetic job results. Only
// needs.<job>.result and needs.changes.outputs.app are understood; anything else fails, so the
// step cannot start reading an input this simulation does not model.
func renderVerdictEnv(t *testing.T, env map[string]any, results map[string]string, app string) []string {
	t.Helper()
	var out []string
	for key, raw := range env {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("verdict env %s is %T", key, raw)
		}
		rendered := verdictExpression.ReplaceAllStringFunc(value, func(expr string) string {
			inner := strings.TrimSpace(verdictExpression.FindStringSubmatch(expr)[1])
			if inner == "needs.changes.outputs.app" {
				return app
			}
			if job, ok := strings.CutPrefix(inner, "needs."); ok {
				if job, ok := strings.CutSuffix(job, ".result"); ok {
					result, known := results[job]
					if !known {
						t.Fatalf("verdict env %s reads %s, which ci-success does not need", key, job)
					}
					return result
				}
			}
			t.Fatalf("verdict env %s has an expression the simulation does not model: %s", key, expr)
			return ""
		})
		out = append(out, key+"="+rendered)
	}
	return out
}

func TestSentinelGateScripts(t *testing.T) {
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ file, gate string }{{"ci.yml", "ci-success"}, {"helm.yml", "helm-success"}} {
		t.Run(tc.gate, func(t *testing.T) {
			job := requiredJob(t, readWorkflow(t, tc.file), tc.gate)
			needs, ok := job["needs"].([]any)
			if !ok || len(needs) == 0 {
				t.Fatal("missing needs")
			}
			var gate, verdict map[string]any
			for _, raw := range requiredSteps(t, job) {
				step := raw.(map[string]any)
				if step["id"] == "gate" {
					if gate != nil {
						t.Fatal("duplicate gate")
					}
					gate = step
				}
				if step["name"] == verdictStepName {
					if verdict != nil || gate == nil {
						t.Fatal("the verdict step must come once, after the gate")
					}
					verdict = step
				}
			}
			if gate == nil {
				t.Fatal("missing gate step")
			}
			if _, ok := gate["if"]; ok {
				t.Fatal("gate has condition")
			}
			fastPath := tc.gate == "ci-success"
			if fastPath != (verdict != nil) {
				t.Fatalf("%s: verdict step present %v, want %v", tc.gate, verdict != nil, fastPath)
			}
			var verdictEnv map[string]any
			var verdictScript string
			if verdict != nil {
				if _, ok := verdict["if"]; ok {
					t.Fatal("verdict step has condition")
				}
				verdictEnv, _ = verdict["env"].(map[string]any)
				verdictScript, _ = verdict["run"].(string)
				if verdictEnv["CHANGES"] != "${{ needs.changes.result }}" || verdictEnv["APP"] != "${{ needs.changes.outputs.app }}" || verdictScript == "" {
					t.Fatalf("wrong verdict step: %v", verdict)
				}
			}
			env, ok := gate["env"].(map[string]any)
			if !ok || env["NEEDS_JSON"] != "${{ toJSON(needs) }}" || env["EVENT_NAME"] != "${{ github.event_name }}" {
				t.Fatalf("wrong gate environment: %v", env)
			}
			script, ok := gate["run"].(string)
			if !ok || script == "" {
				t.Fatal("missing gate script")
			}
			// check runs the gate and then the verdict step, as ci-success does: a step without an
			// if runs only when the steps before it passed. app is changes' output; it is empty
			// whenever changes did not succeed, as GitHub reports it.
			check := func(t *testing.T, event, app string, overrides map[string]string, wantFail bool) {
				t.Helper()
				results := map[string]string{}
				needsJSON := map[string]map[string]any{}
				for _, raw := range needs {
					name := raw.(string)
					result := "success"
					if override, ok := overrides[name]; ok {
						result = override
					}
					results[name] = result
					needsJSON[name] = map[string]any{"result": result, "outputs": map[string]string{}}
				}
				if results["changes"] != "success" {
					app = ""
				}
				if _, ok := needsJSON["changes"]; ok {
					needsJSON["changes"]["outputs"] = map[string]string{"app": app}
				}
				raw, err := json.Marshal(needsJSON)
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("bash", "-eu", "-o", "pipefail", "-c", script)
				cmd.Env = append(os.Environ(), "NEEDS_JSON="+string(raw), "EVENT_NAME="+event)
				output, err := cmd.CombinedOutput()
				gateFailed := err != nil
				failed := gateFailed
				if !gateFailed && verdict != nil {
					cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", verdictScript)
					cmd.Env = append(os.Environ(), renderVerdictEnv(t, verdictEnv, results, app)...)
					var verdictOutput []byte
					verdictOutput, err = cmd.CombinedOutput()
					output = append(output, verdictOutput...)
					failed = err != nil
				}
				if failed != wantFail {
					t.Fatalf("event %s app %q results %v: error %v output %s", event, app, results, err, output)
				}
				if wantFail && gateFailed {
					for name, result := range overrides {
						deferred := result == "skipped" && fastPath && (appSuites[name] || name == fastPathGuardJob)
						if result != "success" && !deferred && !strings.Contains(string(output), name+"="+result) {
							t.Errorf("missing failure %s=%s in %s", name, result, output)
						}
					}
				}
				if wantFail && !gateFailed && !strings.Contains(string(output), "::error::") {
					t.Errorf("verdict failed without an error annotation: %s", output)
				}
			}
			// The ordinary pull request and push: changes says app: 'true', so fast-path-guards is
			// skipped and every suite runs.
			ordinary := map[string]string{}
			fastPathRun := map[string]string{}
			if fastPath {
				ordinary[fastPathGuardJob] = "skipped"
				for name := range appSuites {
					fastPathRun[name] = "skipped"
				}
			}
			check(t, "pull_request", "true", ordinary, false)
			exceptions := map[string]string{"privacy-sweep": "skipped", "actionlint": "skipped", "dependency-review": "skipped"}
			if fastPath {
				pushRun := map[string]string{fastPathGuardJob: "skipped"}
				for name, result := range exceptions {
					pushRun[name] = result
				}
				check(t, "push", "true", pushRun, false)
				check(t, "pull_request", "true", pushRun, true)
				// A fast-path pull request: every suite skipped, fast-path-guards passed.
				check(t, "pull_request", "false", fastPathRun, false)
				guardsSkipped := map[string]string{fastPathGuardJob: "skipped"}
				for name, result := range fastPathRun {
					guardsSkipped[name] = result
				}
				check(t, "pull_request", "false", guardsSkipped, true)
				// changes passed but answered nothing usable.
				for _, app := range []string{"", "maybe"} {
					check(t, "pull_request", app, ordinary, true)
					check(t, "pull_request", app, fastPathRun, true)
				}
				// Every gated suite is skipped only on a verdict, and the guard is in the jq skip set.
				for name := range appSuites {
					t.Run(name+"/skipped/app-true", func(t *testing.T) {
						check(t, "pull_request", "true", map[string]string{fastPathGuardJob: "skipped", name: "skipped"}, true)
					})
					t.Run(name+"/skipped/app-false", func(t *testing.T) {
						check(t, "pull_request", "false", map[string]string{name: "skipped"}, false)
					})
				}
			}
			for _, raw := range needs {
				name := raw.(string)
				for _, result := range []string{"skipped", "failure", "cancelled"} { //nolint:misspell // GitHub Actions uses the result enum cancelled.
					for _, event := range []string{"pull_request", "push"} {
						if tc.gate == "ci-success" && event == "push" && result == "skipped" && exceptions[name] != "" {
							continue
						}
						if result == "skipped" && (appSuites[name] || name == fastPathGuardJob) && fastPath {
							continue // covered above, with the verdict that decides it
						}
						base := ordinary
						if name == fastPathGuardJob {
							base = map[string]string{}
						}
						overrides := map[string]string{name: result}
						for job, r := range base {
							if job != name {
								overrides[job] = r
							}
						}
						t.Run(name+"/"+result+"/"+event, func(t *testing.T) { check(t, event, "true", overrides, true) })
					}
				}
			}
		})
	}
}
