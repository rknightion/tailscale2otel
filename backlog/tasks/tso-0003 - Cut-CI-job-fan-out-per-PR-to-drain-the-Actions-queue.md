---
id: TSO-0003
title: Cut CI job fan-out per PR to drain the Actions queue
status: In Progress
assignee: []
created_date: '2026-08-25 11:28'
updated_date: '2026-08-25 11:28'
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
- [ ] #1 ci.yml's fuzz matrix no longer runs on pull_request events
- [ ] #2 ci.yml's coverage (Codacy) job no longer runs on pull_request events
- [ ] #3 internal/ci/workflowcontract_test.go asserts both lanes are push-only so the change cannot silently regress
- [ ] #4 renovate.json groups non-major gomod updates so the fleet raises ~2 PRs instead of ~15
- [ ] #5 go build ./... && go vet ./... && go test -race ./... green, golangci-lint clean, actionlint clean
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
