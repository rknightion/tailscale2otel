package ci_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
			var gate map[string]any
			for _, raw := range requiredSteps(t, job) {
				step := raw.(map[string]any)
				if step["id"] == "gate" {
					if gate != nil {
						t.Fatal("duplicate gate")
					}
					gate = step
				}
			}
			if gate == nil {
				t.Fatal("missing gate step")
			}
			if _, ok := gate["if"]; ok {
				t.Fatal("gate has condition")
			}
			env, ok := gate["env"].(map[string]any)
			if !ok || env["NEEDS_JSON"] != "${{ toJSON(needs) }}" || env["EVENT_NAME"] != "${{ github.event_name }}" {
				t.Fatalf("wrong gate environment: %v", env)
			}
			script, ok := gate["run"].(string)
			if !ok || script == "" {
				t.Fatal("missing gate script")
			}
			check := func(t *testing.T, event string, overrides map[string]string, wantFail bool) {
				t.Helper()
				results := map[string]map[string]string{}
				for _, raw := range needs {
					name := raw.(string)
					result := "success"
					if override, ok := overrides[name]; ok {
						result = override
					}
					results[name] = map[string]string{"result": result}
				}
				raw, err := json.Marshal(results)
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("bash", "-eu", "-o", "pipefail", "-c", script)
				cmd.Env = append(os.Environ(), "NEEDS_JSON="+string(raw), "EVENT_NAME="+event)
				output, err := cmd.CombinedOutput()
				if (err != nil) != wantFail {
					t.Fatalf("event %s results %s: error %v output %s", event, raw, err, output)
				}
				if wantFail {
					for name, result := range overrides {
						if result != "success" && !strings.Contains(string(output), name+"="+result) {
							t.Errorf("missing failure %s=%s in %s", name, result, output)
						}
					}
				}
			}
			check(t, "pull_request", nil, false)
			exceptions := map[string]string{"ac-evidence": "skipped", "privacy-sweep": "skipped", "backlog-freeze": "skipped"}
			if tc.gate == "ci-success" {
				check(t, "push", exceptions, false)
				check(t, "pull_request", exceptions, true)
			}
			for _, raw := range needs {
				name := raw.(string)
				for _, result := range []string{"skipped", "failure", "cancelled"} { //nolint:misspell // GitHub Actions uses the result enum cancelled.
					for _, event := range []string{"pull_request", "push"} {
						if tc.gate == "ci-success" && event == "push" && result == "skipped" && exceptions[name] != "" {
							continue
						}
						t.Run(name+"/"+result+"/"+event, func(t *testing.T) { check(t, event, map[string]string{name: result}, true) })
					}
				}
			}
		})
	}
}
