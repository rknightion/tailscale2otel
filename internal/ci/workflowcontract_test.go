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
	"bufio"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	repoDir     = "../.."
	workflowDir = repoDir + "/.github/workflows"
)

type justRecipe struct {
	dependencies []string
	body         []string
}

// justRecipes parses the small, stable subset of just syntax this repository
// uses. Tests cannot shell out to just: the root-module test job must remain
// meaningful before setup-just runs, and a missing local binary must never turn
// a workflow contract into a skip.
func justRecipes(t *testing.T) map[string]justRecipe {
	t.Helper()
	f, err := os.Open(filepath.Join(repoDir, "justfile"))
	if err != nil {
		t.Fatalf("open justfile: %v", err)
	}
	defer f.Close()

	header := regexp.MustCompile(`^([a-z0-9-]+)([^:]*):(.*)$`)
	recipes := make(map[string]justRecipe)
	var current string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if match := header.FindStringSubmatch(line); match != nil {
			current = match[1]
			if _, exists := recipes[current]; exists {
				t.Fatalf("justfile declares recipe %q more than once", current)
			}
			recipes[current] = justRecipe{dependencies: strings.Fields(match[3])}
			continue
		}
		if current != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			recipe := recipes[current]
			recipe.body = append(recipe.body, strings.TrimSpace(line))
			recipes[current] = recipe
			continue
		}
		if strings.TrimSpace(line) != "" {
			current = ""
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan justfile: %v", err)
	}
	if len(recipes) == 0 {
		t.Fatal("justfile parser found no recipes; every recipe-backed assertion would be vacuous")
	}
	return recipes
}

func expandedRecipeBody(t *testing.T, names ...string) string {
	t.Helper()
	recipes := justRecipes(t)
	var lines []string
	visiting := make(map[string]bool)
	expanded := make(map[string]bool)
	var expand func(string)
	expand = func(name string) {
		if expanded[name] {
			return
		}
		if visiting[name] {
			t.Fatalf("justfile dependency cycle reaches %q", name)
		}
		recipe, ok := recipes[name]
		if !ok {
			t.Fatalf("justfile has no recipe %q", name)
		}
		visiting[name] = true
		for _, dependency := range recipe.dependencies {
			expand(dependency)
		}
		lines = append(lines, recipe.body...)
		visiting[name] = false
		expanded[name] = true
	}
	for _, name := range names {
		expand(name)
	}
	if len(lines) == 0 {
		t.Fatalf("recipes %v expand to an empty body", names)
	}
	return strings.Join(lines, "\n")
}

func workflowScript(steps []any) string {
	var script strings.Builder
	for _, raw := range steps {
		if step, ok := raw.(map[string]any); ok {
			if run, ok := step["run"].(string); ok {
				script.WriteString(run)
				script.WriteByte('\n')
			}
		}
	}
	return script.String()
}

// recipeBackedWorkflowBody keeps the ordered migration green while workflows
// still carry their legacy command, but always returns the justfile recipe body
// for the substantive assertion. Consequently, deleting a command from a
// recipe fails even during the transition; the legacy workflow cannot mask it.
func recipeBackedWorkflowBody(t *testing.T, steps []any, recipe string, legacy ...string) string {
	t.Helper()
	script := workflowScript(steps)
	invokesRecipe := false
	for line := range strings.SplitSeq(script, "\n") {
		trimmed := strings.TrimSpace(line)
		invocation := "just " + recipe
		if trimmed == invocation || strings.HasPrefix(trimmed, invocation+" ") ||
			strings.HasPrefix(trimmed, invocation+"\t") {
			invokesRecipe = true
			break
		}
	}
	if !invokesRecipe {
		for _, command := range legacy {
			if !strings.Contains(script, command) {
				t.Fatalf("workflow neither invokes `just %s` nor carries transitional command %q", recipe, command)
			}
		}
	}
	return expandedRecipeBody(t, recipe)
}

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
		{"fuzz-scheduled.yml", "**Scheduled fuzzing**"},
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
	body := recipeBackedWorkflowBody(t, steps, "test", "go test -race ./...")
	if !strings.Contains(body, "go test -race") || !strings.Contains(body, "./...") {
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

// goModules discovers every Go module in the repository by walking for go.mod,
// so the assertions below cannot go stale when a module is added.
func goModules(t *testing.T) []string {
	t.Helper()
	var out []string
	root := "../.."
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			// .claude holds agent git worktrees — each is a full checkout of this
			// same repository, so walking into one rediscovers every module under a
			// second path and reports all of them as missing from the CI matrices.
			// The paths are real and the go.mod files are real, which is why the
			// failure reads as a genuine coverage gap rather than as noise; the
			// modules simply are not this checkout's. Local-only, never in CI, and
			// nothing under it is tracked.
			case ".git", ".claude", "node_modules", "dist", ".capture":
				return filepath.SkipDir
			}
			return nil
		}
		if info.Name() != "go.mod" {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk for go.mod: %v", err)
	}
	if len(out) < 2 {
		t.Fatalf("found %d modules (%v); the repository has a root module plus tool modules, so "+
			"this discovery is broken and every assertion built on it is vacuous", len(out), out)
	}
	return out
}

// matrixModules returns the module list of a matrix job in ci.yml.
func matrixModules(t *testing.T, jobName string) []string {
	t.Helper()
	doc := readWorkflow(t, "ci.yml")
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no jobs mapping")
	}
	job, ok := jobs[jobName].(map[string]any)
	if !ok {
		t.Fatalf("ci.yml has no %q job", jobName)
	}
	strategy, ok := job["strategy"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no strategy block", jobName)
	}
	matrix, ok := strategy["matrix"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no matrix block", jobName)
	}
	raw, ok := matrix["module"].([]any)
	if !ok {
		t.Fatalf("%s matrix has no module list", jobName)
	}
	var out []string
	for _, m := range raw {
		s, ok := m.(string)
		if !ok {
			t.Fatalf("%s matrix module entry is %T, want a string", jobName, m)
		}
		out = append(out, s)
	}
	return out
}

// There is no go.work, so `go build ./...` / `go vet ./...` / `go test -race ./...`
// / `govulncheck ./...` run from the repository root CANNOT reach a nested module —
// each tool module is a separate go.mod by design, precisely so it never affects
// the main module's build. The consequence is that a tool module is only verified
// by whatever explicitly names it, and adding a module verifies nothing by default.
//
// This asserts every discovered module is named by a CI matrix that verifies it, so
// a fifth module cannot be added and silently go unchecked (#437).
func TestEveryGoModuleIsCoveredByCIVerification(t *testing.T) {
	modules := goModules(t)

	for _, job := range []string{"lint", "module-verify"} {
		t.Run(job, func(t *testing.T) {
			covered := map[string]bool{}
			for _, m := range matrixModules(t, job) {
				covered[m] = true
			}
			for _, m := range modules {
				// The root module is spelled "." in a matrix, and has its own
				// dedicated build/test/vuln jobs besides.
				name := m
				if name == "." && job == "module-verify" {
					continue
				}
				if !covered[name] {
					t.Errorf("module %q is not in ci.yml's %q matrix. Without a go.work, nothing "+
						"run from the repo root reaches it, so it is verified only by what names "+
						"it explicitly.", name, job)
				}
			}
		})
	}
}

func TestEveryGoModuleIsCoveredByJustVerification(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoDir, "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^modules := "([^"]+)"$`).FindSubmatch(b)
	if match == nil {
		t.Fatal("justfile has no parseable modules := line; all module-coverage assertions would be vacuous")
	}
	covered := make(map[string]bool)
	for _, module := range strings.Fields(string(match[1])) {
		covered[module] = true
	}
	for _, module := range goModules(t) {
		if !covered[module] {
			t.Errorf("module %q is absent from justfile's modules list; without a go.work, just check cannot reach it", module)
		}
	}
}

// The per-module verification job has to actually verify something. A matrix that
// names every module but only builds it would satisfy the test above while
// checking almost nothing, so the steps themselves are asserted.
func TestModuleVerifyRunsTheFullPerModuleGate(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs, _ := doc["jobs"].(map[string]any)
	job, ok := jobs["module-verify"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no module-verify job")
	}
	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatal("module-verify has no steps list")
	}
	body := strings.Join([]string{
		recipeBackedWorkflowBody(t, steps, "test-modules", "go build ./...", "go test -race ./..."),
		recipeBackedWorkflowBody(t, steps, "vet", "go vet ./..."),
		recipeBackedWorkflowBody(t, steps, "tidy-check", "go mod tidy"),
		recipeBackedWorkflowBody(t, steps, "vuln", "govulncheck ./..."),
	}, "\n")
	for _, want := range []struct {
		cmd string
		why string
	}{
		{"go build -C", "a module that does not compile is not verified"},
		{"go vet -C", "vet catches the mistakes the compiler accepts"},
		{"go test -C", "a module with no test leg has no behavioral guarantee at all"},
		{"go mod tidy -C", "Renovate bumps a dependency in one module and leaves the nested ones stale; " +
			"a tidy-diff is what catches it, and tools/configcheck/go.sum really had drifted"},
		{"govulncheck ./...", "the root govulncheck cannot reach a nested module without a go.work"},
	} {
		if !strings.Contains(body, want.cmd) {
			t.Errorf("no step in module-verify invokes %q — %s", want.cmd, want.why)
		}
	}
}

// The root module needs the same tidy-diff as the tool modules. It is not in the
// module-verify matrix (it has dedicated jobs), so the check has to be asserted
// where it actually lives — and it was missing: three root requirements were
// marked `// indirect` while being imported directly, and nothing caught it (#437).
func TestRootModuleTidyIsChecked(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs, _ := doc["jobs"].(map[string]any)
	job, ok := jobs["build-test"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no build-test job")
	}
	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatal("build-test has no steps list")
	}
	body := recipeBackedWorkflowBody(t, steps, "tidy-check", "go mod tidy")
	if strings.Contains(body, "go mod tidy -C") {
		return
	}
	t.Error("build-test never invokes `go mod tidy`. The root module is excluded from the " +
		"module-verify matrix, so without a tidy leg here nothing checks it — which is how three " +
		"directly-imported requirements sat marked `// indirect`.")
}

// Every generated artifact needs a fail-on-diff gate, or a stale one ships
// looking fine. Four have had one for a while (docs/metrics.md, docs/env-vars.md,
// the chart README and values.schema.json); the generated Grafana artifacts had
// none at all until #438.
//
// This asserts the gate exists and is REQUIRED, rather than asserting a diff —
// the diff itself is what the CI job checks.
func TestGeneratedGrafanaArtifactsAreDriftGated(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no jobs mapping")
	}

	const gate = "dashboards-drift"
	job, ok := jobs[gate].(map[string]any)
	if !ok {
		t.Fatalf("ci.yml has no %q job. deploy/grafana/tailscale2otel-tailnet.json, "+
			"deploy/grafana/tailscale2otel-health.json and deploy/alerts/grafana-managed/ "+
			"are generated, so without a fail-on-diff gate a stale one ships silently.", gate)
	}

	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatalf("%s has no steps list", gate)
	}
	body := recipeBackedWorkflowBody(t, steps, "gen-check", "regen-generated.sh dashboards", "git diff --exit-code")
	if !strings.Contains(body, "regen-generated.sh dashboards") {
		t.Errorf("%s does not regenerate via scripts/regen-generated.sh — the CI gate and the "+
			"local command must be the same code path, or one of them drifts", gate)
	}
	if !strings.Contains(body, "if ! git diff --exit-code") {
		t.Errorf("%s regenerates but never diffs, so it would pass on stale artifacts", gate)
	}
	for _, dir := range []string{"deploy/grafana", "deploy/alerts"} {
		if !strings.Contains(body, dir) {
			t.Errorf("%s does not check %s for drift", gate, dir)
		}
	}

	success, _ := jobs["ci-success"].(map[string]any)
	needs, _ := success["needs"].([]any)
	for _, n := range needs {
		if s, ok := n.(string); ok && s == gate {
			return
		}
	}
	t.Errorf("ci-success does not require %q, so the drift gate would report without blocking", gate)
}

// promtool parses every PromQL expression in the Prometheus-format rule file,
// which nothing in Go does — the reference tests in internal/catalog check that
// metric NAMES exist, not that the expressions are valid PromQL. It cannot check
// the Grafana-managed file (promtool does not understand that schema), which is
// why that one has a structural test in internal/catalog instead (#438).
func TestPrometheusRulesAreCheckedByPromtool(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs, _ := doc["jobs"].(map[string]any)
	job, ok := jobs["dashboards-drift"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no dashboards-drift job")
	}
	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatal("dashboards-drift has no steps list")
	}
	body := recipeBackedWorkflowBody(t, steps, "rules-check", "promtool check rules", "promtool test rules")
	if !strings.Contains(body, "promtool check rules") {
		t.Error("dashboards-drift never runs `promtool check rules`, so no rule expression is " +
			"ever parsed by a Prometheus-native checker")
	}
	// Prometheus-only deployments consume the committed rendering directly, and
	// the semantic fixtures must execute that same shipped file. Checking a
	// throwaway sibling would allow the deliverable to drift while CI stayed green.
	if !strings.Contains(body, "promtool check rules deploy/alerts/prometheus/tailscale2otel.rules.yaml") {
		t.Error("dashboards-drift does not check the committed Prometheus rule artifact, " +
			"so a stale or malformed deployable file can ship")
	}
	if strings.Contains(body, ".generated-prom-rules.yaml") {
		t.Error("dashboards-drift still executes a throwaway Prometheus rendering instead of " +
			"deploy/alerts/prometheus/tailscale2otel.rules.yaml")
	}
	// The tarball is verified against the release's own published sums rather than
	// a checksum pasted here, which would silently rot on the next version bump.
	if !strings.Contains(workflowScript(steps), "sha256sums.txt") {
		t.Error("the promtool download is not verified against the release's sha256sums.txt")
	}
	// `check rules` proves the expressions parse; it does not run them. A rule can
	// be valid PromQL and still never fire, or fire forever — the already-expired
	// key case in deploy/alerts/tests is exactly that shape (#407).
	if !strings.Contains(body, "promtool test rules") {
		t.Error("dashboards-drift parses the rules but never executes them against fixtures, " +
			"so a rule that is valid PromQL and semantically wrong still ships")
	}
}

// promqlcheck is the only thing that parses the DASHBOARD's expressions and the
// Grafana provisioning rules — promtool understands neither. Adding the module to
// the lint/module-verify matrices makes it build and test; it does not make it RUN
// against the artifacts. Verified-but-never-invoked is exactly the shape of #437, so
// assert the invocation on its own line rather than as a loose substring, because
// remediation prose in an error message has satisfied a substring check here before.
func TestDashboardsDriftRunsPromqlcheck(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs, _ := doc["jobs"].(map[string]any)
	job, ok := jobs["dashboards-drift"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no dashboards-drift job")
	}
	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatal("dashboards-drift has no steps list")
	}
	body := recipeBackedWorkflowBody(t, steps, "promql", "go run -C tools/promqlcheck")
	if strings.Contains(body, "go run -C tools/promqlcheck") {
		return
	}
	t.Error("dashboards-drift never invokes `go run -C tools/promqlcheck`, so every panel " +
		"query and Grafana rule expression goes unparsed. Note `go run ./tools/promqlcheck` " +
		"from the root does NOT work — it is a separate module.")
}

// Guards the guard for `promtool test rules`: the CI step globs
// deploy/alerts/tests/*.yaml, and a glob that matches nothing makes promtool exit 0.
func TestPrometheusRuleFixturesExist(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(repoDir, "deploy/alerts/tests", "*.yaml"))
	if err != nil {
		t.Fatalf("globbing rule fixtures: %v", err)
	}
	if len(matches) == 0 {
		t.Error("deploy/alerts/tests holds no fixtures, so the `promtool test rules` step " +
			"matches no files and passes while executing nothing")
	}
}

// Both Python generators ship unittest suites, and for a long time CI ran neither:
// deploy/grafana/gen/test_ingest_freshness.py passed locally and was referenced by
// nothing in .github/, scripts/ or .githooks/, so it caught nothing (#481). A test
// suite that no gate runs is worse than none, because its presence reads as coverage.
// The drift gate alone is not a substitute — it proves the artifact matches the
// generator, not that the generator is correct.
func TestPythonGeneratorTestsRunInCI(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs, _ := doc["jobs"].(map[string]any)
	job, ok := jobs["dashboards-drift"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no dashboards-drift job")
	}
	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatal("dashboards-drift has no steps list")
	}
	body := recipeBackedWorkflowBody(t, steps, "test-python", "unittest discover")
	if !strings.Contains(body, "unittest discover") {
		t.Error("dashboards-drift never runs `python3 -m unittest discover`, so the generator " +
			"unit tests execute nowhere and only pass by being run by hand")
	}
	// scripts/ carries the release-completeness gate (#442), whose own logic
	// would otherwise run for the first time during a release — the one moment
	// it is being relied upon.
	for _, dir := range []string{"deploy/grafana/gen", "deploy/alerts/gen", "scripts"} {
		if !strings.Contains(body, dir) {
			t.Errorf("dashboards-drift does not run the unit tests in %s", dir)
		}
	}
}

// Guards the guard: TestPythonGeneratorTestsRunInCI only proves the workflow invokes
// unittest against those directories. It cannot tell whether the directories actually
// contain tests — `unittest discover` over a directory with no test module exits 0 and
// reports success, which would make the CI step a no-op that reads as coverage.
func TestPythonGeneratorDirectoriesContainTests(t *testing.T) {
	for _, dir := range []string{"deploy/grafana/gen", "deploy/alerts/gen", "scripts"} {
		matches, err := filepath.Glob(filepath.Join(repoDir, dir, "test_*.py"))
		if err != nil {
			t.Fatalf("globbing %s: %v", dir, err)
		}
		if len(matches) == 0 {
			t.Errorf("%s contains no test_*.py, so the CI unittest step over it discovers "+
				"nothing and passes vacuously", dir)
		}
	}
}

// Every fuzz target in the tree must run in BOTH lanes: the bounded post-merge matrix
// in ci.yml, so a shallow crasher surfaces within a push of landing, and the weekly
// scheduled matrix, so the deeper search reaches it at all. A target added to neither
// is dead code that only runs its seed corpus, and nothing else would say so (#434).
func TestEveryFuzzTargetRunsInBothMatrices(t *testing.T) {
	declared := fuzzTargetsInTree(t)
	if len(declared) < 9 {
		t.Fatalf("found %d (package, target) fuzz pairs in the tree, expected at least 9: %v",
			len(declared), sortedPairs(declared))
	}

	for _, lane := range []struct{ workflow, job string }{
		{"ci.yml", "fuzz"},
		{"fuzz-scheduled.yml", "fuzz"},
	} {
		t.Run(lane.workflow, func(t *testing.T) {
			listed := fuzzMatrixTargets(t, lane.workflow, lane.job)
			for pair := range declared {
				if !listed[pair] {
					t.Errorf("%s does not fuzz %s. A target absent from a lane runs only its "+
						"seed corpus there.", lane.workflow, pair)
				}
			}
			for pair := range listed {
				if !declared[pair] {
					t.Errorf("%s fuzzes %s, which does not exist in the tree — `go test -fuzz` would "+
						"match no target and the step would pass while testing nothing", lane.workflow, pair)
				}
			}
		})
	}
}

// sortedPairs renders a pair set deterministically for a failure message.
func sortedPairs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The scheduled lane is advisory: it must be schedule/dispatch only, so no fork PR
// can reach it, and it must not be a required check.
func TestScheduledFuzzLaneIsAdvisoryOnly(t *testing.T) {
	doc := readWorkflow(t, "fuzz-scheduled.yml")
	for name := range triggerBlock(t, doc, "fuzz-scheduled.yml") {
		switch name {
		case "schedule", "workflow_dispatch":
		default:
			t.Errorf("fuzz-scheduled.yml declares trigger %q. A 15-minute-per-target matrix on a "+
				"PR trigger would be both a merge blocker and a fork-reachable compute sink.", name)
		}
	}
}

// fuzzTargetsInTree returns the set of "./package FuzzTarget" pairs in the
// repository.
//
// The key is the PAIR, not the target name: FuzzProcessorProcessAll exists in both
// internal/flowlog and internal/audit, so a map keyed on the name alone silently
// loses one of them — which is exactly what the first version of this test did, and
// it reported eight targets where there are nine.
func fuzzTargetsInTree(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	root := "../.."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .claude is skipped for the same reason goModules skips it: it holds
			// agent git worktrees, each a full checkout of this repository, so
			// walking in rediscovers every fuzz target under a second path.
			if name := d.Name(); name == ".git" || name == ".claude" || name == "testdata" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		pkg, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, m := range fuzzFuncRE.FindAllStringSubmatch(string(body), -1) {
			out["./"+filepath.ToSlash(pkg)+" "+m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk for fuzz targets: %v", err)
	}
	return out
}

var fuzzFuncRE = regexp.MustCompile(`(?m)^func (Fuzz[A-Za-z0-9_]*)\(f \*testing\.F\)`)

// fuzzMatrixTargets returns the set of "package target" pairs a workflow's fuzz job
// lists, in the same spelling fuzzTargetsInTree uses.
func fuzzMatrixTargets(t *testing.T, workflow, job string) map[string]bool {
	t.Helper()
	doc := readWorkflow(t, workflow)
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no jobs mapping", workflow)
	}
	def, ok := jobs[job].(map[string]any)
	if !ok {
		t.Fatalf("%s has no %q job", workflow, job)
	}
	strategy, ok := def["strategy"].(map[string]any)
	if !ok {
		t.Fatalf("%s/%s has no strategy", workflow, job)
	}
	matrix, ok := strategy["matrix"].(map[string]any)
	if !ok {
		t.Fatalf("%s/%s has no matrix", workflow, job)
	}
	include, ok := matrix["include"].([]any)
	if !ok {
		t.Fatalf("%s/%s matrix has no include list", workflow, job)
	}
	out := map[string]bool{}
	for _, entry := range include {
		em, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("%s/%s matrix entry is %T", workflow, job, entry)
		}
		target, _ := em["target"].(string)
		pkg, _ := em["package"].(string)
		if target == "" || pkg == "" {
			t.Fatalf("%s/%s matrix entry is missing target or package: %v", workflow, job, em)
		}
		out[pkg+" "+target] = true
	}
	return out
}

// --- runaway-job containment (#439) ------------------------------------------
//
// A job with no `timeout-minutes` inherits GitHub's implicit 360-minute default.
// That is not merely untidy: a hung job holds a runner slot against the
// ACCOUNT-WIDE concurrency cap, which is shared across every repository on the
// account. A wedged job in a sibling repo has previously starved this repo's CI
// into a queue that looks green-but-never-starts, and the workflows most exposed
// to it are the ones that fetch untrusted upstream code on a schedule.
//
// So the contract is: every job that this repository can actually bound, is
// bounded.

// workflowFiles returns every workflow filename, so a NEW workflow is covered by
// these assertions the day it is added rather than the day someone remembers.
func workflowFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Fatalf("read %s: %v", workflowDir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := e.Name(); strings.HasSuffix(n, ".yml") || strings.HasSuffix(n, ".yaml") {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		t.Fatalf("found no workflow files under %s", workflowDir)
	}
	sort.Strings(out)
	return out
}

// jobsOf returns a workflow's jobs mapping.
func jobsOf(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no jobs mapping", name)
	}
	return jobs
}

// reusableCallJobs are the jobs this repository CANNOT bound, because they are
// `uses:` calls to a reusable workflow and GitHub rejects `timeout-minutes` on
// that job form — the key is simply not part of the schema for a job with
// `uses:`. Six of these target rknightion/.github, so bounding them means
// editing that repository, not this one.
//
// This set is frozen deliberately. It is not a suppression list to grow when a
// test is inconvenient: converting a `uses:` job into a `steps:` job removes the
// exemption's only justification, and TestExemptionsAreOnlyForReusableCallJobs
// below fails in that direction too.
var reusableCallJobs = map[string]bool{
	"actionlint.yml/actionlint":               true,
	"arm-automerge.yml/arm":                   true, // reusable call: GitHub does not allow timeout-minutes on uses jobs
	"auto-rc.yml/rc":                          true, // reusable call: GitHub does not allow timeout-minutes on uses jobs
	"auto-rc.yml/publish":                     true, // local reusable call; the called workflow owns its job timeouts
	"auto-rc.yml/binaries":                    true, // reusable call: GitHub does not allow timeout-minutes on uses jobs
	"codeql.yml/codeql":                       true,
	"dependency-review.yml/dependency-review": true,
	"docker-security.yml/docker-security":     true,
	"ghcr-cleanup.yml/prune":                  true, // reusable call: GitHub does not allow timeout-minutes on uses jobs
	"publish.yml/image":                       true,
	"release-please.yml/publish":              true,
	"release-please.yml/edge":                 true,
	"release-please.yml/binaries":             true,
	"scorecard.yml/scorecard":                 true,
	"zizmor.yml/zizmor":                       true,
}

// maxJobTimeout bounds the bound. A timeout of 360 is the implicit default
// written out longhand and containment nothing; anything above this is a
// misunderstanding of what the field is for.
const maxJobTimeout = 60

func TestEveryBoundableJobHasATimeout(t *testing.T) {
	for _, wf := range workflowFiles(t) {
		doc := readWorkflow(t, wf)
		for job, raw := range jobsOf(t, doc, wf) {
			def, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s/%s is %T, want a mapping", wf, job, raw)
			}
			key := wf + "/" + job
			if _, isUses := def["uses"]; isUses {
				continue // covered by TestExemptionsAreOnlyForReusableCallJobs
			}
			timeout, ok := def["timeout-minutes"]
			if !ok {
				t.Errorf("%s has no timeout-minutes; a job with steps CAN be bounded, and an "+
					"unbounded job that hangs holds a runner slot against the account-wide "+
					"concurrency cap for up to 6 hours", key)
				continue
			}
			n, ok := timeout.(int)
			if !ok {
				t.Errorf("%s timeout-minutes is %T (%v), want an integer", key, timeout, timeout)
				continue
			}
			if n < 1 || n > maxJobTimeout {
				t.Errorf("%s timeout-minutes is %d, want 1..%d; %d is long enough that the "+
					"timeout stops being containment", key, n, maxJobTimeout, n)
			}
		}
	}
}

func TestExemptionsAreOnlyForReusableCallJobs(t *testing.T) {
	seen := map[string]bool{}
	for _, wf := range workflowFiles(t) {
		doc := readWorkflow(t, wf)
		for job, raw := range jobsOf(t, doc, wf) {
			def, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s/%s is %T, want a mapping", wf, job, raw)
			}
			key := wf + "/" + job
			_, isUses := def["uses"]
			if isUses {
				seen[key] = true
				if !reusableCallJobs[key] {
					t.Errorf("%s is a `uses:` job but is not in reusableCallJobs; add it there "+
						"WITH the reason, so the set stays a record of what cannot be bounded "+
						"rather than a list of what someone skipped", key)
				}
				continue
			}
			if reusableCallJobs[key] {
				t.Errorf("%s is exempted from the timeout rule but is no longer a `uses:` job — "+
					"it has steps, so it CAN take timeout-minutes. Remove the exemption and "+
					"bound it", key)
			}
		}
	}
	for key := range reusableCallJobs {
		if !seen[key] {
			t.Errorf("reusableCallJobs lists %q, which no longer exists; a stale exemption "+
				"silently widens the rule's blind spot", key)
		}
	}
}

// TestSupersedableLanesCancelOrSerialize asserts every workflow declares how
// concurrent runs of itself behave.
//
// The two correct answers differ by trigger, and the distinction matters:
//
//   - A pull_request lane SHOULD cancel superseded runs. Pushing three times to a
//     branch should not burn three full CI runs against the shared cap.
//   - A scheduled or release lane MUST NOT cancel; it should serialize. Killing a
//     drift scan or a release mid-flight loses the run's signal, and two
//     overlapping scans of the same lane race to file the same tracking issue.
func TestSupersedableLanesCancelOrSerialize(t *testing.T) {
	for _, wf := range workflowFiles(t) {
		doc := readWorkflow(t, wf)
		conc, ok := doc["concurrency"].(map[string]any)
		if !ok {
			t.Errorf("%s has no top-level concurrency block; without one, overlapping runs of "+
				"the same lane both consume runner slots and can race each other", wf)
			continue
		}
		if group, _ := conc["group"].(string); group == "" {
			t.Errorf("%s concurrency has no group expression", wf)
		}
		cancel, ok := conc["cancel-in-progress"].(bool)
		if !ok {
			t.Errorf("%s concurrency has no explicit cancel-in-progress; leave nothing to the "+
				"default here, the right answer differs per trigger", wf)
			continue
		}
		trigger := triggerBlock(t, doc, wf)
		_, scheduled := trigger["schedule"]
		_, isPR := trigger["pull_request"]
		switch {
		case scheduled && cancel:
			t.Errorf("%s is a scheduled lane with cancel-in-progress: true; canceling a "+
				"half-finished scan discards its signal and can leave a tracking issue "+
				"half-written", wf)
		case isPR && !cancel:
			t.Errorf("%s is a pull_request lane with cancel-in-progress: false; superseded "+
				"pushes should not each burn a full run against the shared cap", wf)
		}
	}
}

// TestCIRetryIsFailClosed exercises scripts/ci-retry.sh directly.
//
// A retry wrapper is exactly the kind of helper that is never noticed until the
// day it is wrong, and the dangerous failure is silent: a wrapper that exits 0
// after exhausting its attempts converts every gate it wraps into a no-op while
// the logs still look busy. So the load-bearing assertion here is the FAILING
// case, not the succeeding one.
func TestCIRetryIsFailClosed(t *testing.T) {
	script, err := filepath.Abs(filepath.Join(repoDir, "scripts", "ci-retry.sh"))
	if err != nil {
		t.Fatalf("resolve script: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("scripts/ci-retry.sh is missing: %v", err)
	}

	run := func(t *testing.T, attempts string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(script, args...)
		cmd.Env = append(os.Environ(),
			"CI_RETRY_ATTEMPTS="+attempts,
			"CI_RETRY_DELAY=0",
		)
		out, err := cmd.CombinedOutput()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run %s: %v", script, err)
		}
		return string(out), code
	}

	t.Run("a command that never succeeds still fails the step", func(t *testing.T) {
		out, code := run(t, "3", "sh", "-c", "exit 7")
		if code != 7 {
			t.Errorf("exit status = %d, want 7 (the last attempt's status); a retry helper "+
				"that swallows the failure silently disables every gate it wraps\n%s", code, out)
		}
		if !strings.Contains(out, "giving up after 3 attempt(s)") {
			t.Errorf("output does not say it gave up, so a run that burned its attempts is "+
				"indistinguishable from a clean pass in the log:\n%s", out)
		}
	})

	t.Run("it retries rather than giving up on the first failure", func(t *testing.T) {
		// Fails on attempt 1, succeeds on attempt 2, via a marker file that
		// only exists after the first run.
		marker := filepath.Join(t.TempDir(), "attempted")
		out, code := run(t, "3", "sh", "-c",
			"if [ -e "+marker+" ]; then exit 0; else touch "+marker+"; exit 1; fi")
		if code != 0 {
			t.Errorf("exit status = %d, want 0: the second attempt succeeds\n%s", code, out)
		}
		if !strings.Contains(out, "succeeded on attempt 2/3") {
			t.Errorf("a run that only passed on retry must say so, otherwise a persistently "+
				"flaky dependency looks healthy:\n%s", out)
		}
	})

	t.Run("one attempt means one attempt", func(t *testing.T) {
		out, code := run(t, "1", "sh", "-c", "exit 3")
		if code != 3 {
			t.Errorf("exit status = %d, want 3\n%s", code, out)
		}
		if strings.Contains(out, "retrying") {
			t.Errorf("CI_RETRY_ATTEMPTS=1 must not retry:\n%s", out)
		}
	})

	t.Run("a missing command is a usage error, not a retried one", func(t *testing.T) {
		out, code := run(t, "3")
		if code != 2 {
			t.Errorf("exit status = %d, want 2 for no command\n%s", code, out)
		}
	})
}

// TestTransientFetchesAreRetried keeps the wrapper attached to the operations it
// was added for. Wrapping is easy to lose in a later edit of the surrounding
// script, and nothing else would notice.
func TestTransientFetchesAreRetried(t *testing.T) {
	for _, tc := range []struct{ workflow, job, needle string }{
		{"api-drift.yml", "spec-drift", "api.tailscale.com/api/v2?outputOpenapiSchema=true"},
		{"ci.yml", "dashboards-drift", "prometheus/releases/download"},
	} {
		t.Run(tc.workflow, func(t *testing.T) {
			doc := readWorkflow(t, tc.workflow)
			def, ok := jobsOf(t, doc, tc.workflow)[tc.job].(map[string]any)
			if !ok {
				t.Fatalf("%s has no %q job", tc.workflow, tc.job)
			}
			steps, ok := def["steps"].([]any)
			if !ok {
				t.Fatalf("%s/%s has no steps", tc.workflow, tc.job)
			}
			var found bool
			for _, raw := range steps {
				step, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				script, _ := step["run"].(string)
				if !strings.Contains(script, tc.needle) {
					continue
				}
				found = true
				for _, line := range strings.Split(script, "\n") {
					if !strings.Contains(line, "curl ") {
						continue
					}
					if !strings.Contains(line, "ci-retry.sh") {
						t.Errorf("%s/%s fetches over the network without scripts/ci-retry.sh:\n\t%s",
							tc.workflow, tc.job, strings.TrimSpace(line))
					}
					if !strings.Contains(line, "--max-time") {
						t.Errorf("%s/%s curl has no --max-time, so a stalled connection hangs "+
							"until the job timeout rather than failing fast into a retry:\n\t%s",
							tc.workflow, tc.job, strings.TrimSpace(line))
					}
				}
			}
			if !found {
				t.Fatalf("%s/%s no longer contains a step fetching %q; this guard is now "+
					"asserting nothing", tc.workflow, tc.job, tc.needle)
			}
		})
	}
}

// TestIANARegistryHasAFreshnessLane (#440).
//
// internal/portservice embeds a trimmed copy of IANA's service-name registry so
// port lookup works offline. That copy has no drift gate by construction — it is
// not one of the generated artifacts regen-generated.sh rebuilds — and its
// staleness is invisible at runtime: an unregistered port and a port missing
// from a stale table both report no service name.
//
// It went stale exactly that way once, generated at the feature commit and never
// again. This asserts the scheduled lane that now watches it still exists and
// still points at the right file, because deleting a lane nobody looks at is
// silent.
func TestIANARegistryHasAFreshnessLane(t *testing.T) {
	const (
		wf  = "iana-freshness.yml"
		csv = "internal/portservice/service-names-port-numbers.csv"
	)
	doc := readWorkflow(t, wf)

	if _, ok := triggerBlock(t, doc, wf)["schedule"]; !ok {
		t.Errorf("%s has no schedule; an on-demand-only freshness check is one nobody runs", wf)
	}

	var scripts strings.Builder
	for _, raw := range jobsOf(t, doc, wf) {
		def, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		steps, _ := def["steps"].([]any)
		for _, s := range steps {
			step, ok := s.(map[string]any)
			if !ok {
				continue
			}
			if run, _ := step["run"].(string); run != "" {
				scripts.WriteString(run + "\n")
			}
			if uses, _ := step["uses"].(string); uses != "" {
				scripts.WriteString(uses + "\n")
			}
			if with, ok := step["with"].(map[string]any); ok {
				if label, _ := with["lane-label"].(string); label != "" {
					scripts.WriteString(label + "\n")
				}
			}
		}
	}
	body := scripts.String()
	for _, want := range []string{
		csv,                    // it compares the committed copy
		"iana.org/assignments", // against the live registry
		"gen.go",               // via the deterministic generator, not an ad-hoc parser
		"iana-drift",           // and reports under its own lane label
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s no longer references %q; the lane may still run while checking "+
				"nothing", wf, want)
		}
	}

	// The generator is deliberately non-fatal on a failed download so
	// `go generate` works offline (#128). In THIS lane that would be a silent
	// pass: no fetch, no diff, green run, nothing learned. The lane must fetch
	// separately and hand the file over with -raw.
	// Match the generator INVOCATION, not the bare flag: the fetched file is
	// named iana-raw.csv, so a substring search for "-raw" matches the filename
	// and passes whether or not the flag is ever passed. It did exactly that on
	// the first draft of this test.
	if !strings.Contains(body, "gen.go -raw") {
		t.Errorf("%s does not pass -raw to the generator, so a failed download would be "+
			"reported as 'registry is current' — gen.go swallows fetch errors by design", wf)
	}

	if _, err := os.Stat(filepath.Join(repoDir, csv)); err != nil {
		t.Errorf("%s is missing: %v", csv, err)
	}
}

// TestNoMatrixJobUpsertsATrackingIssue (#435).
//
// report-drift upserts by reading `gh issue list --label <lane> --state open`
// and creating an issue when the read comes back empty. That is check-then-act
// with no lock, which is safe only while at most one job per label can be in
// flight. A job running under `strategy.matrix` breaks that assumption: its legs
// run concurrently, and a genuine upstream break usually fails ALL of them, so
// both legs read "no open issue" before either has created one and two issues
// are filed under the same label.
//
// The fix is structural — fan the verdicts out, aggregate them, and let exactly
// one job speak — so this asserts the structure rather than the symptom. A
// future lane that grows a matrix and keeps its reporting inline would
// reintroduce the identical bug, and nothing else would notice: duplicate issues
// look like two real findings.
func TestNoMatrixJobUpsertsATrackingIssue(t *testing.T) {
	for _, wf := range workflowFiles(t) {
		doc := readWorkflow(t, wf)
		for job, raw := range jobsOf(t, doc, wf) {
			def, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			strategy, _ := def["strategy"].(map[string]any)
			if _, hasMatrix := strategy["matrix"]; !hasMatrix {
				continue
			}
			steps, _ := def["steps"].([]any)
			for _, s := range steps {
				step, ok := s.(map[string]any)
				if !ok {
					continue
				}
				uses, _ := step["uses"].(string)
				if strings.Contains(uses, "report-drift") || strings.Contains(uses, "resolve-drift") {
					t.Errorf("%s/%s runs under a matrix AND calls %s. Its legs run "+
						"concurrently and the composite upserts by list-then-create with no "+
						"lock, so two failing legs can file two issues under one label. "+
						"Publish each leg's verdict as an artifact and aggregate in a single "+
						"downstream job instead.", wf, job, uses)
				}
			}
		}
	}
}

// TestOnlyOneJobPerLaneCanWriteIssues is the permission-shaped half of the same
// contract. Least privilege here is not decoration: a job that cannot write to
// the tracker cannot race with the job that can, so the narrower permission is
// what makes the structure above enforceable rather than merely conventional.
func TestOnlyOneJobPerLaneCanWriteIssues(t *testing.T) {
	for _, wf := range workflowFiles(t) {
		doc := readWorkflow(t, wf)
		var writers []string
		for job, raw := range jobsOf(t, doc, wf) {
			def, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			perms, ok := def["permissions"].(map[string]any)
			if !ok {
				continue
			}
			if issues, _ := perms["issues"].(string); issues == "write" {
				writers = append(writers, job)
			}
		}
		if len(writers) > 1 {
			sort.Strings(writers)
			t.Errorf("%s grants issues:write to more than one job (%s); concurrent writers to "+
				"one lane label race on the tracking issue", wf, strings.Join(writers, ", "))
		}
	}
}

// TestReleaseCompletenessIsVerified (#442).
//
// The release is created visible immediately and three independent jobs then
// attach assets to it in parallel with no ordering. Nothing read the result
// back, so a failed uploader left a permanently incomplete public release behind
// a green workflow — twice (#174, #176).
//
// The specific thing worth guarding is the `if:` condition. A verification job
// that inherits the default `success()` runs only when every uploader already
// worked, which is exactly when it has nothing to find. It must run on
// `always()` and gate on release_created instead.
func TestReleaseCompletenessIsVerified(t *testing.T) {
	const wf = "release-please.yml"
	doc := readWorkflow(t, wf)
	job, ok := jobsOf(t, doc, wf)["verify-release"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no verify-release job; nothing checks that a published release "+
			"actually carries its assets", wf)
	}

	cond, _ := job["if"].(string)
	if !strings.Contains(cond, "always()") {
		t.Errorf("verify-release has `if: %s`. Without always() it inherits success() and "+
			"runs only when every uploader already succeeded — it would never see the "+
			"failure it exists to catch.", cond)
	}
	if !strings.Contains(cond, "release_created") {
		t.Errorf("verify-release has `if: %s`, which does not gate on release_created; it "+
			"would run on every push to main and fail against a release that was never cut",
			cond)
	}

	needs, _ := job["needs"].([]any)
	got := map[string]bool{}
	for _, n := range needs {
		if s, ok := n.(string); ok {
			got[s] = true
		}
	}
	for _, want := range []string{"release-please", "publish", "binaries"} {
		if !got[want] {
			t.Errorf("verify-release does not need %q, so it can read the release back "+
				"before that job has finished uploading and report a false incompleteness",
				want)
		}
	}

	var script strings.Builder
	steps, _ := job["steps"].([]any)
	for _, raw := range steps {
		if step, ok := raw.(map[string]any); ok {
			if run, _ := step["run"].(string); run != "" {
				script.WriteString(run + "\n")
			}
		}
	}
	if !strings.Contains(script.String(), "check_release_assets.py") {
		t.Error("verify-release does not invoke scripts/check_release_assets.py")
	}
}

// TestDocumentedInstallCommandsAreChecked (#445).
//
// The install commands in README.md and docs/ are the first thing anyone runs,
// and nothing validated them: the chart render tests build their own --set flags
// rather than reading the documented ones, and no workflow executes or checks a
// command taken from any documentation file.
//
// That is how `--set-string secrets.*` — plural, against a chart whose key is
// `secret` — survived in the quick start. Helm accepts an unknown --set path
// silently, so the documented command installed the chart with no credentials.
func TestDocumentedInstallCommandsAreChecked(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	job, ok := jobsOf(t, doc, "ci.yml")["docs-catalog"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no docs-catalog job")
	}
	steps, _ := job["steps"].([]any)
	body := recipeBackedWorkflowBody(t, steps, "docs-check", "check_doc_commands.py")
	if !strings.Contains(body, "check_doc_commands.py") {
		t.Error("docs-catalog does not run scripts/check_doc_commands.py, so a documented " +
			"install command can reference a chart value or environment variable that does " +
			"not exist and nothing fails")
	}
	// The checker is only as good as the generated schemas it reads; if either
	// is missing it exits 2 rather than passing, but a missing file should be
	// caught here rather than at release time.
	for _, f := range []string{
		"deploy/helm/tailscale2otel/values.schema.json",
		"docs/env-vars.md",
	} {
		if _, err := os.Stat(filepath.Join(repoDir, f)); err != nil {
			t.Errorf("%s is missing; check_doc_commands.py validates against it: %v", f, err)
		}
	}
}

// nonGatingCILanes are ci.yml's jobs that are deliberately absent from
// ci-success's `needs`. Both are advisory by design — `fuzz` explores for NEW
// crashers (nondeterministic; the seed corpora gate inside go test -race) and
// `coverage` reports to Codacy (a Codacy outage must never block a merge).
//
// Because they gate nothing, running them on `pull_request` buys no signal a
// merge depends on, and it is not free: this account is on GitHub Free, so the
// whole fleet shares ONE pool of 20 concurrent standard-runner jobs. The fuzz
// matrix alone is 9 of ci.yml's 25 jobs. Multiplied across a Renovate batch,
// these two lanes were the difference between a queue that drains and one that
// does not — 15 open PRs x 30 jobs against 20 slots, with automerge unable to
// free a slot because it was waiting on the same queue.
//
// They still run on every push to main, which is where a post-merge advisory
// signal belongs.
var nonGatingCILanes = map[string]bool{
	"coverage": true,
	"fuzz":     true,
}

// pushOnlyGuard is the exact expression a non-gating lane must carry, and it is
// compared for EQUALITY, not containment. A substring check is worthless here:
// `github.event_name == 'pull_request' || github.event_name == 'push'` contains
// the guard verbatim while being true on exactly the event the guard exists to
// exclude. Verified by mutation — the containment version of this test passed
// that string.
//
// Equality means a lane that legitimately needs a compound condition trips this
// test. That is the intended outcome: widening when one of these two runs is a
// decision about the account's shared runner budget, not a refactor.
const pushOnlyGuard = "github.event_name == 'push'"

// A non-gating lane that runs on pull_request spends the account's scarcest
// shared resource on a check no merge can depend on. This test is the reason
// the comment above cannot quietly stop being true: the `if` guards are one
// deletion away from restoring a 40%-larger PR fan-out, and nothing else in the
// repository would notice, because removing them makes MORE checks report.
func TestNonGatingCILanesDoNotRunOnPullRequests(t *testing.T) {
	doc := readWorkflow(t, "ci.yml")
	jobs := jobsOf(t, doc, "ci.yml")

	success, ok := jobs["ci-success"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no ci-success job — branch protection depends on it")
	}
	rawNeeds, ok := success["needs"].([]any)
	if !ok {
		t.Fatalf("ci-success needs is %T, want a list", success["needs"])
	}
	gating := map[string]bool{"ci-success": true}
	for _, n := range rawNeeds {
		s, ok := n.(string)
		if !ok {
			t.Fatalf("ci-success needs entry is %T, want a string", n)
		}
		gating[s] = true
	}

	for job, raw := range jobs {
		def, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("ci.yml/%s is %T, want a mapping", job, raw)
		}

		// Every ci.yml job is either required, or listed here as deliberately
		// advisory. A third category is how an unbounded lane creeps back in.
		if !gating[job] && !nonGatingCILanes[job] {
			t.Errorf("ci.yml declares job %q, which is neither in ci-success's needs nor in "+
				"nonGatingCILanes. Add it to ci-success if it should gate merges, or to "+
				"nonGatingCILanes (and guard it with %q) if it is advisory — an unlisted "+
				"advisory job runs on every PR while blocking nothing.", job, pushOnlyGuard)
			continue
		}
		if !nonGatingCILanes[job] {
			continue
		}

		if gating[job] {
			t.Errorf("ci.yml/%s is in nonGatingCILanes but ci-success requires it. A lane cannot "+
				"be advisory and required at once; if it should gate merges, drop the push-only "+
				"guard too, or every PR blocks on a check that never reports.", job)
			continue
		}

		cond, ok := def["if"].(string)
		if !ok {
			t.Errorf("ci.yml/%s has no `if` guard, so it runs on pull_request while sitting "+
				"outside ci-success's needs — it consumes runner slots from the account-wide "+
				"concurrency cap and no merge can depend on the result. Guard it with %q.",
				job, pushOnlyGuard)
			continue
		}
		if strings.TrimSpace(cond) != pushOnlyGuard {
			t.Errorf("ci.yml/%s has `if: %s`, want exactly %q. Naming the event is not the same "+
				"as restricting to it — an OR'd condition mentions `push` and still runs on "+
				"every pull request, which is the whole cost this guard removes.",
				job, cond, pushOnlyGuard)
		}
	}

	// A lane listed here but deleted from the workflow leaves a stale exemption
	// that would silently bless the next job to reuse the name.
	for job := range nonGatingCILanes {
		if _, declared := jobs[job]; !declared {
			t.Errorf("nonGatingCILanes lists %q, which ci.yml no longer declares; drop the "+
				"stale entry rather than leaving it to bless a future job of the same name", job)
		}
	}
}
