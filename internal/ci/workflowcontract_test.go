// Package ci_test holds guard tests over the repository's OWN CI contract.
//
// These assert facts about `.github/workflows/*.yml` that the README states as
// prose. Prose drifts silently — a cron edited in a workflow does not touch the
// table describing it, and nothing failed when two of those descriptions were
// wrong for months (#436). A test that reads the workflow files makes the
// documentation load-bearing.
//
// They deliberately assert the CONTRACT (cadence, gating, runner class,
// credential source) rather than the mechanics, so ordinary workflow maintenance
// does not trip them.
package ci_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const workflowDir = "../../.github/workflows"

func readWorkflow(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(workflowDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return doc
}

// triggerBlock returns a workflow's `on:` mapping.
//
// It fails rather than returning empty when the key is missing: a nil map would
// make every assertion built on it vacuously pass, which is the failure mode
// these tests exist to prevent.
func triggerBlock(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	trigger, ok := doc["on"]
	if !ok {
		t.Fatalf("%s has no `on:` trigger block; yaml.v3 follows the YAML 1.2 core schema, so "+
			"a bare `on` stays the string key \"on\" rather than becoming a boolean", name)
	}
	m, ok := trigger.(map[string]any)
	if !ok {
		t.Fatalf("%s trigger block is %T, want a mapping", name, trigger)
	}
	return m
}

// crons returns every schedule cron expression in a workflow.
func crons(t *testing.T, doc map[string]any, name string) []string {
	t.Helper()
	trigger := triggerBlock(t, doc, name)
	sched, ok := trigger["schedule"]
	if !ok {
		return nil
	}
	entries, ok := sched.([]any)
	if !ok {
		t.Fatalf("%s schedule is %T, want a list", name, sched)
	}
	var out []string
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("%s schedule entry is %T, want a mapping", name, e)
		}
		c, ok := em["cron"].(string)
		if !ok {
			t.Fatalf("%s schedule entry has no cron string", name)
		}
		out = append(out, c)
	}
	return out
}

// cadence classifies a cron as the README describes it. Only the two shapes this
// repository actually uses are recognized; anything else must be described
// explicitly rather than guessed at, so it fails loudly.
func cadence(t *testing.T, cron, workflow string) string {
	t.Helper()
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		t.Fatalf("%s cron %q does not have five fields", workflow, cron)
	}
	dayOfWeek := fields[4]
	switch {
	case dayOfWeek == "*":
		return "daily"
	case regexp.MustCompile(`^[0-6]$`).MatchString(dayOfWeek):
		return "weekly"
	default:
		t.Fatalf("%s cron %q has an unrecognized day-of-week %q; describe it in the README "+
			"explicitly and teach this test the shape", workflow, cron, dayOfWeek)
		return ""
	}
}

// The README's "API drift CI" table states a cadence for each scheduled lane.
// Two of them said "weekly" while their crons ran DAILY (#436) — nothing failed,
// because the only thing asserting the cadence was the sentence itself.
func TestReadmeAPIDriftCadenceMatchesTheCrons(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(readme)

	for _, lane := range []struct {
		workflow string
		// row is the README table row's leading cell, used to locate the claim.
		row string
	}{
		{"api-drift.yml", "**OpenAPI drift**"},
		{"clientlib-main.yml", "**Client-lib tracking**"},
		{"live-contract.yml", "**Live contract**"},
	} {
		t.Run(lane.workflow, func(t *testing.T) {
			got := crons(t, readWorkflow(t, lane.workflow), lane.workflow)
			if len(got) != 1 {
				t.Fatalf("%s has %d schedules, want exactly one", lane.workflow, len(got))
			}
			want := cadence(t, got[0], lane.workflow)

			row := tableRow(t, body, lane.row)
			if !strings.Contains(row, want) {
				t.Errorf("README row %s does not say %q, but %s runs %s (cron %q).\nRow: %s",
					lane.row, want, lane.workflow, want, got[0], row)
			}
		})
	}
}

// tableRow returns the README markdown table row beginning with cell.
func tableRow(t *testing.T, body, cell string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "| "+cell) {
			return line
		}
	}
	t.Fatalf("README has no table row starting with %q", cell)
	return ""
}

// The advisory-versus-gating split is the load-bearing claim in that section: a
// scheduled lane that became a required check would block every PR on upstream's
// behavior, and the fuzz job becoming required would let an unrelated PR trip a
// latent crasher and block Renovate. Neither may appear in ci-success's needs.
func TestScheduledAndFuzzLanesAreNotRequiredChecks(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no jobs mapping")
	}
	success, ok := jobs["ci-success"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no ci-success job — branch protection depends on it")
	}
	rawNeeds, ok := success["needs"].([]any)
	if !ok {
		t.Fatalf("ci-success needs is %T, want a list", success["needs"])
	}
	needs := map[string]bool{}
	for _, n := range rawNeeds {
		s, ok := n.(string)
		if !ok {
			t.Fatalf("ci-success needs entry is %T, want a string", n)
		}
		needs[s] = true
	}

	if needs["fuzz"] {
		t.Error("ci-success requires the fuzz job. Exploratory fuzzing is nondeterministic, so an " +
			"unrelated PR could randomly trip a latent crasher and block merges; each target's " +
			"SEED corpus already runs inside go test -race, which is what gates a known crasher.")
	}
	// A scheduled lane lives in its own file, so it cannot literally be a `needs`
	// entry here — assert the invariant that matters instead: every required job
	// is declared in this same workflow.
	for name := range needs {
		if _, declared := jobs[name]; !declared {
			t.Errorf("ci-success requires job %q, which ci.yml does not declare. A required check "+
				"that never reports leaves every PR blocked forever.", name)
		}
	}
}

// The live lane reaches a real tailnet with real credentials. Two properties keep
// that safe, and both are easy to undo by accident: it must never run on a
// fork-reachable trigger (a `pull_request` trigger would expose the secrets), and
// it must not run on a self-hosted runner — the abandoned self-hosted design is
// exactly what left this lane producing no signal for months (#160).
func TestLiveContractLaneStaysScheduleOnlyAndHosted(t *testing.T) {
	const name = "live-contract.yml"
	doc := readWorkflow(t, name)

	for event := range triggerBlock(t, doc, name) {
		switch event {
		case "schedule", "workflow_dispatch":
		default:
			t.Errorf("%s is triggered by %q. This lane holds Tailscale OAuth credentials as repo "+
				"secrets, which is only safe while no fork-reachable trigger can start it.", name, event)
		}
	}

	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no jobs mapping", name)
	}
	for jobName, raw := range jobs {
		job, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		runsOn, ok := job["runs-on"].(string)
		if !ok {
			continue
		}
		if !strings.HasPrefix(runsOn, "ubuntu-") && !strings.HasPrefix(runsOn, "windows-") &&
			!strings.HasPrefix(runsOn, "macos-") {
			t.Errorf("%s job %q runs on %q. A self-hosted runner on a PUBLIC repo is a risk GitHub "+
				"warns against, and the never-provisioned `tailscale-api` runner is why this lane "+
				"queued forever and was auto-canceled weekly, producing no signal at all (#160).",
				name, jobName, runsOn)
		}
	}
}

// The schema-driven decode tests are the ONE API-drift lane the README says
// gates. They gate by riding the normal root-module test leg rather than having a
// job of their own, so what has to hold is: that leg exists, it really runs the
// whole root module under the race detector, and ci-success requires it. Losing
// any one of the three would leave the README's only gating claim false.
func TestSchemaDrivenDecodeTestsRideAGatedLeg(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no jobs mapping")
	}

	const leg = "build-test"
	job, ok := jobs[leg].(map[string]any)
	if !ok {
		t.Fatalf("ci.yml has no %q job", leg)
	}
	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatalf("%s has no steps list", leg)
	}
	var found bool
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if run, ok := step["run"].(string); ok && strings.Contains(run, "go test -race ./...") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no step in %s runs `go test -race ./...`. The schema-driven decode tests in "+
			"internal/tsapi/contract have no job of their own — they gate only by riding this leg.", leg)
	}

	success, ok := jobs["ci-success"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no ci-success job")
	}
	needs, ok := success["needs"].([]any)
	if !ok {
		t.Fatalf("ci-success needs is %T, want a list", success["needs"])
	}
	for _, n := range needs {
		if s, ok := n.(string); ok && s == leg {
			return
		}
	}
	t.Errorf("ci-success does not require %q, so the only API-drift lane the README calls gating "+
		"would not actually block a merge", leg)
}
