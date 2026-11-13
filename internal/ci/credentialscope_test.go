package ci_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestLiveContractTokenIsStepScoped(t *testing.T) {
	doc := readWorkflow(t, "live-contract.yml")
	for name, scope := range map[string]map[string]any{"workflow": doc, "live job": requiredJob(t, doc, "live")} {
		env, _ := scope["env"].(map[string]any)
		if _, exists := env["TS_API_ACCESS_TOKEN"]; exists {
			t.Errorf("token in %s environment", name)
		}
	}
	expected := map[string]bool{"The minted token must carry read scope": false, "Run live contract": false}
	exchangeFound := false
	for _, raw := range requiredSteps(t, requiredJob(t, doc, "live")) {
		step := raw.(map[string]any)
		name, _ := step["name"].(string)
		script, _ := step["run"].(string)
		if strings.Contains(script, "TS_API_ACCESS_TOKEN") && strings.Contains(script, "GITHUB_ENV") {
			t.Errorf("%s exports token to job environment", name)
		}
		env, _ := step["env"].(map[string]any)
		value, has := env["TS_API_ACCESS_TOKEN"]
		if _, allowed := expected[name]; allowed {
			expected[name] = true
			if !has || value != "${{ steps.exchange.outputs.token }}" {
				t.Errorf("%s lacks scoped token", name)
			}
		} else if has {
			t.Errorf("unexpected token consumer %s", name)
		}
		if step["id"] == "exchange" {
			exchangeFound = true
			mask := strings.Index(script, `echo "::add-mask::$token"`)
			output := strings.Index(script, `echo "token=$token" >> "$GITHUB_OUTPUT"`)
			if mask < 0 || output < mask {
				t.Error("exchange output must follow token mask")
			}
		}
	}
	if !exchangeFound {
		t.Error("missing exchange output step")
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing consumer %s", name)
		}
	}
}

func TestCoverageJobHasNoJobLevelSecret(t *testing.T) {
	job := requiredJob(t, readWorkflow(t, "ci.yml"), "coverage")
	if strings.Contains(fmt.Sprint(job["env"]), "secrets.") {
		t.Error("coverage job exposes secrets to dependency code")
	}
	present, upload := false, false
	for _, raw := range requiredSteps(t, job) {
		step := raw.(map[string]any)
		if step["id"] == "codacy" {
			present = true
			env, _ := step["env"].(map[string]any)
			if env["CODACY_PROJECT_TOKEN"] != "${{ secrets.CODACY_PROJECT_TOKEN }}" {
				t.Error("presence step missing token")
			}
		}
		if step["name"] == "Upload coverage to Codacy" {
			upload = true
			with, _ := step["with"].(map[string]any)
			if step["if"] != "steps.codacy.outputs.present == 'true'" || with["project-token"] != "${{ secrets.CODACY_PROJECT_TOKEN }}" {
				t.Error("upload must use presence output and direct secret")
			}
		}
	}
	if !present || !upload {
		t.Error("missing coverage presence or upload step")
	}
}
