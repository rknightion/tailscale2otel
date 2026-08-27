---
id: TSO-0016
title: Add a release-independent upgrade and rollback checklist
status: Done
assignee:
  - '@codex'
created_date: '2026-08-26 11:02'
updated_date: '2026-08-27 07:01'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - docs/upgrading.md
  - docs/troubleshooting.md
priority: medium
type: docs
ordinal: 20000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The upgrade guide is a useful version-by-version migration ledger but does not give one reusable operational procedure for preflight, rollout verification, persistence, or rollback.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The guide covers persistence backup considerations, -validate, redacted effective-config review, controlled restart, readiness, and post-upgrade OTLP or Prometheus verification
- [x] #2 It distinguishes safe rollback from migrations that require the documented forward-recovery path
- [x] #3 Version-specific entries link to the reusable checklist instead of duplicating it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add one reusable upgrade-and-rollback checklist, link version-specific entries to it, and preserve forward-only recovery for flow-store adoption.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The reusable upgrade and rollback checklist is committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522 and the docs checker passed. Resume with exact-head CI and an operational exercise when desired.

Final evidence: documentation checker, integrated GATE, and exact-head CI run 33047209645 passed. The live campaign exercised controlled restart, readiness, Prometheus verification, and an Alloy restart with persistent recovery.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Upgrade and rollback guidance is committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending exact-head CI and operational proof.

Completion: verified by documentation checks, exact-head CI, and live restart/readiness exercises.
<!-- SECTION:FINAL_SUMMARY:END -->
