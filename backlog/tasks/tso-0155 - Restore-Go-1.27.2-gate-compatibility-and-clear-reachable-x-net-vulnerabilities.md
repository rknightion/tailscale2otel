---
id: TSO-0155
title: Restore Go 1.27.2 gate compatibility and clear reachable x/net vulnerabilities
status: In Progress
assignee:
  - '@loop18'
created_date: '2026-10-09 12:45'
labels: []
dependencies: []
priority: high
type: chore
ordinal: 155000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The loop18 Go-floor candidate exposes a deterministic pinned golangci-lint export-data-v5 incompatibility and five reachable golang.org/x/net v0.59.0 vulnerabilities fixed in v0.60.0. These block the unchanged gate before the scheduled WAL build can be accepted.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Pinned linter parses Go 1.27.2 export data locally and CI uses the matching supported pin without changing gate coverage
- [ ] #2 All five module vulnerability scans pass with patched x/net wherever required and tidy is stable
- [ ] #3 just check passes on the combined Go-floor prerequisite candidate without gate, assertion or recipe weakening
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Diagnose current upstream compatibility release; update only necessary tool version pins and vulnerable module version/sums, retain gate and test semantics, then security review and sharded CodeRabbit before landing.
<!-- SECTION:PLAN:END -->
