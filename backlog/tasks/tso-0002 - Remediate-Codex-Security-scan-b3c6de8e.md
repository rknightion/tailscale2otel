---
id: TSO-0002
title: Remediate Codex Security scan b3c6de8e
status: Done
assignee:
  - '@codex'
created_date: '2026-08-25 09:27'
updated_date: '2026-08-25 11:15'
labels: []
dependencies: []
references:
  - Codex Security scan b3c6de8e-d3d0-434a-bfe4-e6c5c764e02f
priority: high
type: bug
ordinal: 2000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Fix and verify all 19 confirmed findings from Codex Security scan b3c6de8e-d3d0-434a-bfe4-e6c5c764e02f against revision 1f1175fcc1d4470bae00458083a58f695a5280bf. Preserve documented receiver modes and legitimate operator workflows while closing each reported security boundary.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 All six ingress and local HTTP findings are fixed and regression-tested
- [x] #2 All four retained-state and filesystem findings are fixed and regression-tested
- [x] #3 All six outbound credential, transport, URL, and TLS findings are fixed and regression-tested
- [x] #4 All three Helm deployment findings are fixed and validated by deterministic rendering
- [x] #5 The complete repository gate and CodeRabbit security review pass before commit and push
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Run contract: docs/superpowers/2026-08-25-security-remediation-goal.md. Execute child tasks sequentially in order .01 through .04, then one integrated review and final gate.

Clear the shared repository gate by registering the five reusable-workflow call jobs, adding trigger-appropriate concurrency to arm-automerge and ghcr-cleanup, then rerun focused and full gates before atomically finalizing the parent and all four subtasks.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
All 19 scan findings are fixed and independently reviewed. CodeRabbit second pass had no Critical or Warning findings. Build, vet, lint, affected-package race suite, govulncheck, generators, Helm, and Windows cross-build pass. The literal full race suite remains blocked only by seven unchanged internal/ci workflow-contract assertions for workflow files outside this remediation diff; acceptance criterion 5 and DoD 1 remain unchecked.

Cleared the unrelated current-HEAD workflow-contract blocker by registering five reusable-call jobs and adding trigger-appropriate concurrency to arm-automerge and ghcr-cleanup. Full build, vet, race suite, lint, and actionlint passed on 2026-08-25.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Remediated and regression-tested all 19 findings from scan b3c6de8e-d3d0-434a-bfe4-e6c5c764e02f. Security implementation is complete; tracker remains Parked solely at the unrelated current-HEAD workflow-contract gate.

The repository gate is green and all acceptance criteria are satisfied.

The earlier Parked handoff is superseded by this gate fix; the task and all subtasks are Done.
<!-- SECTION:FINAL_SUMMARY:END -->
