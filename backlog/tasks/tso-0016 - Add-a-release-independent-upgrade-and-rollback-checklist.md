---
id: TSO-0016
title: Add a release-independent upgrade and rollback checklist
status: To Do
assignee: []
created_date: '2026-08-26 11:02'
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
- [ ] #1 The guide covers persistence backup considerations, -validate, redacted effective-config review, controlled restart, readiness, and post-upgrade OTLP or Prometheus verification
- [ ] #2 It distinguishes safe rollback from migrations that require the documented forward-recovery path
- [ ] #3 Version-specific entries link to the reusable checklist instead of duplicating it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
