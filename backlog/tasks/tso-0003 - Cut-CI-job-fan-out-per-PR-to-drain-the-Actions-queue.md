---
id: TSO-0003
title: Cut CI job fan-out per PR to drain the Actions queue
status: Done
assignee: []
created_date: '2026-08-25 11:28'
updated_date: '2026-08-25 12:17'
labels: []
dependencies: []
ordinal: 7000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The account is on GitHub Free (public repo, user account): 20 concurrent standard-runner jobs shared across ALL rknightion repos. Each PR fires ~30 jobs (ci.yml 25 + helm.yml 2 + actionlint + zizmor + dependency-review). With 15 open Renovate PRs that is ~450 jobs against a 20-slot pool, and opnsense2otel competes for the same pool. Result: a standing queue tens of runs deep.

Only two checks are required by the main ruleset: ci-success and helm-success. 9 of ci.yml's 25 jobs are the exploratory 'fuzz' matrix and 1 is 'coverage (Codacy)' - all 10 are deliberately outside ci-success.needs, so they gate nothing while consuming 40% of every PR's job budget.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 ci.yml's fuzz matrix no longer runs on pull_request events
- [x] #2 ci.yml's coverage (Codacy) job no longer runs on pull_request events
- [x] #3 internal/ci/workflowcontract_test.go asserts both lanes are push-only so the change cannot silently regress
- [x] #4 renovate.json groups non-major gomod updates so the fleet raises ~2 PRs instead of ~15
- [x] #5 go build ./... && go vet ./... && go test -race ./... green, golangci-lint clean, actionlint clean
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. ci.yml: add `if: github.event_name == 'push'` to the `fuzz` job (9 jobs) and the `coverage` job (1 job). Both are already outside ci-success.needs, so a skipped job cannot affect the required check. Keep both matrices intact so TestEveryFuzzTargetRunsInBothMatrices still holds.
2. internal/ci/workflowcontract_test.go: add a test asserting every non-gating ci.yml lane carries a push-only guard. Watch it fail first (TDD).
3. renovate.json: add a packageRule grouping non-major gomod updates into one 'Go dependencies' PR, ordered so the OpenTelemetry lockstep group and the tools/** indirect rules still win.
4. Gate: go build/vet/test -race, golangci-lint run, actionlint.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed in 0e8c72a on main.

**ci.yml** — `fuzz` (9 legs) and `coverage (Codacy)` (1 leg) now carry `if: github.event_name == 'push'`. Both were already outside `ci-success.needs`, so a skipped job cannot affect the required check; verified `ci-success` still reports with them skipped by the `if: always()` + needs list it already had. Both matrices left intact so TestEveryFuzzTargetRunsInBothMatrices still holds.

**Guard test** — TestNonGatingCILanesDoNotRunOnPullRequests. Written failing first (both lanes reported unguarded), then made to pass. Compares the `if` for EQUALITY, not containment: mutation testing proved the containment draft passed `github.event_name == 'pull_request' || github.event_name == 'push'`, which contains the guard string verbatim while being true on the exact event it exists to exclude. Mutation matrix after the fix — guard removed: FAIL; guard OR-weakened: FAIL; clean: PASS. The test also fails on any ci.yml job that is neither in ci-success.needs nor in the nonGatingCILanes set, and on a stale set entry naming a deleted job.

**renovate.json** — new first packageRule groups gomod minor/patch/digest into one 'Go dependencies' PR. Placed first because for groupName the LAST matching rule wins, so the OpenTelemetry lockstep group and the tools/** indirect rules keep overriding it. Majors stay ungrouped (a Go major is a distinct module path = a real code change). Validated with renovate-config-validator: 'Config validated successfully'.

**Prose** — README's advisory/gating paragraph, ci.yml's and fuzz-scheduled.yml's header comments, and TestEveryFuzzTargetRunsInBothMatrices' doc comment all said 'PR-time' fuzzing; corrected to post-merge.

Gate: go build, go vet, `go test -race ./...` exit 0, `golangci-lint run` 0 issues, `actionlint` exit 0.

Effect: ~30 jobs per PR to ~20; a 15-PR Renovate cycle from ~450 queued jobs to ~60 (fewer PRs x fewer jobs each).

Note: the working tree held another session's uncommitted work (tso-0004, prometheus.max_requests_in_flight). Only this task's five files plus this task file were staged; that work was left untouched.

AC 1, 2 and 4 NOT yet checked — no live evidence available at time of writing.

Every queued PR run is still on a base 1 commit BEHIND 0e8c72a (verified via /compare behind_by=1 for all three open CI PR runs), so none exercises the change yet. The most recent pre-change PR run is the clean BASELINE: run 32843597335, head f3e4bcd, behind_by=1 — 25 total jobs, 10 of them fuzz+coverage. The after-number to compare against is 15 total, 0 fuzz+coverage.

The main push run for ef3120f (32843887781, behind_by=0) was still `pending` after ~20 min of polling; the account-wide queue was 22 at that point, down from 30 at the start of the session. Nothing can be observed until the pre-change backlog clears, which is the very condition this task addresses.

RESUME BOUNDARY: once any PR run with behind_by=0 against 0e8c72a has jobs, assert total==15 and fuzz+coverage==0, then check AC 1 and 2. For AC 4, confirm the next Renovate cycle opens a single 'Go dependencies' PR rather than ~15 (renovate.json already passes renovate-config-validator; only the PR-count outcome is unobserved).

Draining the queue faster is possible by cancelling the stale 1-behind Renovate PR runs — not done, as it is a bulk mutation of the user's CI that was not requested.

LIVE VERIFICATION COMPLETE.

Post-change PR runs (all four contain 0e8c72a, behind_by=0) each show **17 job entries, of which 2 are skipped -> 15 executing**, against the pre-change baseline of **25 executing** (run 32843597335, behind_by=1):

| branch | run | executing | skipped |
| --- | --- | --- | --- |
| renovate/go-dependencies | 32845965800 | 15 | fuzz, coverage |
| renovate/grafana-alloy-1.x | 32845983780 | 15 | fuzz, coverage |
| renovate/step-security-harden-runner-2.x | 32845997891 | 15 | fuzz, coverage |
| renovate/anthropics-claude-code-action-1.x | 32845924924 | 15 | fuzz, coverage |

Better than modelled: the skipped `fuzz` entry reports with its name UNEXPANDED — literally `fuzz (${{ matrix.package }} ${{ matrix.target }})`, one entry, conclusion `skipped`. A job-level `if` is evaluated BEFORE matrix expansion, so the 9 legs are never created at all rather than created-then-skipped. 9 legs -> 0 runner slots.

AC 4 verified by outcome, not just by validator: Renovate ran and folded the individual Go module PRs into a single **#572 renovate/go-dependencies**. The five superseded branches (modernc.org-sqlite, prometheus, maxminddb, protobuf, grpc) are DELETED (branches API 404) and their PRs closed. Open Renovate PRs went 15 -> 5 (#573 alloy, #572 go-dependencies, #568 harden-runner, #558 claude-code-action, #533 opentelemetry). The OpenTelemetry lockstep group survived as its own PR, confirming the rule ordering is correct.

Stale-run cleanup (user-authorised): 8 orphaned runs force-cancelled — 1 superseded main Helm run, 1 behind release-please CI run, and 6 runs against deleted/superseded Renovate branches. `gh run cancel` reported 'Request to cancel submitted' and the runs stayed `queued`; only POST .../force-cancel actually killed them. A queued run appears to ignore plain cancel.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Cut per-PR CI fan-out from 25 executing jobs to 15 by guarding ci.yml's two non-gating lanes (the 9-leg `fuzz` matrix and `coverage (Codacy)`, both already outside ci-success.needs) with `if: github.event_name == 'push'`, and grouped non-major gomod updates into one Renovate PR. Verified live on four post-change PR runs: 15 executing jobs each, with `fuzz` skipped before matrix expansion so its 9 legs are never created. Renovate PRs went 15 -> 5. Guarded by TestNonGatingCILanesDoNotRunOnPullRequests, which compares the `if` for equality rather than containment — mutation testing showed the containment draft accepting an OR'd condition that was still true on pull_request. Gate: go build/vet/test -race exit 0, golangci-lint 0 issues, actionlint exit 0.
<!-- SECTION:FINAL_SUMMARY:END -->
