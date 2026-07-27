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
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
			case ".git", "node_modules", "dist", ".capture":
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
	var script strings.Builder
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if run, ok := step["run"].(string); ok {
			script.WriteString(run)
			script.WriteString("\n")
		}
	}
	// Match each command as an INVOKED line, not merely as a substring of the
	// concatenated script. A substring match is satisfied by the command's name
	// appearing inside an error message — which is exactly how a first version of
	// this test passed after the `go mod tidy` invocation had been removed, since
	// the remediation message still said "run 'go mod tidy'".
	invoked := func(cmd string) bool {
		for _, line := range strings.Split(script.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), cmd) {
				return true
			}
		}
		return false
	}
	for _, want := range []struct {
		cmd string
		why string
	}{
		{"go build ./...", "a module that does not compile is not verified"},
		{"go vet ./...", "vet catches the mistakes the compiler accepts"},
		{"go test -race ./...", "a module with no test leg has no behavioral guarantee at all"},
		{"go mod tidy", "Renovate bumps a dependency in one module and leaves the nested ones stale; " +
			"a tidy-diff is what catches it, and tools/configcheck/go.sum really had drifted"},
		{"govulncheck ./...", "the root govulncheck cannot reach a nested module without a go.work"},
	} {
		if !invoked(want.cmd) {
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
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		run, ok := step["run"].(string)
		if !ok {
			continue
		}
		for _, line := range strings.Split(run, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "go mod tidy") {
				return
			}
		}
	}
	t.Error("build-test never invokes `go mod tidy`. The root module is excluded from the " +
		"module-verify matrix, so without a tidy leg here nothing checks it — which is how three " +
		"directly-imported requirements sat marked `// indirect`.")
}

// Every generated artifact needs a fail-on-diff gate, or a stale one ships
// looking fine. Four have had one for a while (docs/metrics.md, docs/env-vars.md,
// the chart README and values.schema.json); the two generated Grafana artifacts
// had none at all until #438.
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
		t.Fatalf("ci.yml has no %q job. deploy/grafana/tailscale2otel.json and "+
			"deploy/alerts/tailscale2otel.grafana-rules.yaml are generated, so without a "+
			"fail-on-diff gate a stale one ships silently.", gate)
	}

	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatalf("%s has no steps list", gate)
	}
	var script strings.Builder
	for _, raw := range steps {
		if step, ok := raw.(map[string]any); ok {
			if run, ok := step["run"].(string); ok {
				script.WriteString(run)
				script.WriteString("\n")
			}
		}
	}
	body := script.String()
	if !strings.Contains(body, "regen-generated.sh dashboards") {
		t.Errorf("%s does not regenerate via scripts/regen-generated.sh — the CI gate and the "+
			"local command must be the same code path, or one of them drifts", gate)
	}
	if !strings.Contains(body, "git diff --exit-code") {
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
	var script strings.Builder
	for _, raw := range steps {
		if step, ok := raw.(map[string]any); ok {
			if run, ok := step["run"].(string); ok {
				script.WriteString(run)
				script.WriteString("\n")
			}
		}
	}
	body := script.String()
	if !strings.Contains(body, "promtool check rules") {
		t.Error("dashboards-drift never runs `promtool check rules`, so no PromQL expression in " +
			"deploy/alerts/tailscale2otel.rules.yaml is ever parsed")
	}
	// The tarball is verified against the release's own published sums rather than
	// a checksum pasted here, which would silently rot on the next version bump.
	if !strings.Contains(body, "sha256sums.txt") {
		t.Error("the promtool download is not verified against the release's sha256sums.txt")
	}
}

// Every fuzz target in the tree must run in BOTH lanes: the bounded PR matrix, so a
// shallow crasher is found on the pull request, and the weekly scheduled matrix, so
// the deeper search reaches it at all. A target added to neither is dead code that
// only runs its seed corpus, and nothing else would say so (#434).
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
			if name := d.Name(); name == ".git" || name == "testdata" || name == "node_modules" {
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
