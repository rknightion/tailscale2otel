# CI gates and scheduled lanes

Read before editing a workflow, diagnosing a red check that `just check` does not reproduce, or
refreshing the vendored OpenAPI spec.

- Running `promqlcheck` against the ARTIFACTS is a different question from the module legs and easy
  to conflate. Building and unit-testing the tool proves nothing about the dashboards and rules the
  repo ships; the artifact run parses every expression and resolves each `$variable` against what is
  in scope where the panel actually sits. In CI it lives in the dashboards-drift job, not
  `module-verify`, and it has landed dozens of real failures with every local gate green.
- The schema-driven decode tests and the `oas` classifier tests run inside `go test -race ./...` and
  do gate PRs. The `fuzz` job in `ci.yml` is deliberately NOT in `ci-success.needs`: a new crasher is
  nondeterministic, so gating it would let an unrelated PR randomly block merges. Each target's seed
  corpus rides the gated leg, so a known crasher still blocks. "Decode fuzz gates" is true of the
  tests and false of the job.
- The scheduled lanes - `api-drift.yml` daily, `live-contract.yml` daily, `clientlib-main.yml`
  weekly, `fuzz-scheduled.yml` weekly - are advisory: on detection they open a deduped tracking
  issue and fail the scheduled run, but never block PRs. `internal/ci/workflowcontract_test.go`
  asserts those cadences and the gating split against the workflow files, because the README stated
  two of them wrong for months while nothing failed.
- `live-contract.yml` authenticates by Workload Identity Federation, so no Tailscale secret is
  stored in GitHub at all. Read its header comment before editing it, and do not "restore" the
  self-hosted runner it replaced - that runner was never provisioned, so the lane produced no signal
  at all, and standing one up on a public repo is a risk GitHub warns against.
- The vendored OpenAPI spec is `spec/tailscale-api.json`, not the repo root, and there is no `.yaml`
  copy. Refreshing it is manual and is how you *acknowledge* a detected drift; `api-drift.yml`
  fetches the live spec to `/tmp` for comparison and never writes back. `internal/oas.ParseSpec`
  keeps **only** `get` operations and silently drops every other verb, so `Spec.Ops` never contains
  a write.
- A clean local `actionlint` does NOT mean the actionlint CI lane will pass. actionlint shells out
  to whatever `shellcheck` is on PATH, and the runner's is OLDER and reports MORE - local shellcheck
  0.11.0 does not emit SC2015 (`A && B || C` is not if-then-else) at all, so a workflow edit can be
  clean locally on two actionlint versions and still fail the lane. The version gap runs the reverse
  of the usual way, so "my tool is up to date" is not reassurance. In a workflow `run:` block, prefer
  a plain `if` over `A && B || C`.
- A CI test with a wall-clock margin flakes on a loaded runner: remove the timing dependency
  (`testing/synctest`, or a fake-driver context probe asserting which context an operation
  inherited), never widen the margin, and never rerun until green or merge past a flake. `gh run
  rerun` replays the merge commit GitHub froze when the run was created; check its parents, and use
  `gh pr update-branch <n>` for a run that contains current `main`.
- auto-rc cuts a full publish on every green CI on `main`, after CI has reported done: a closing check
  covers the workflows that fire off CI, and an auto-rc red is infrastructure (proxy ingestion lag,
  agent timeouts, registry not-found, cosign guard) until proven otherwise.
- The owner merges the release PR by hand: no run merges it, tags, or touches release-please config;
  runs validate from the rc images. The bang goes after the scope, `feat(config)!:`; `feat!(config):`
  is silently dropped by release-please, so repair it additively with a second commit.
- RBAC proofs (`auth can-i --as`) go through the direct cluster endpoint: the tailnet API proxy answers
  as the operator and returns a false `yes`.
