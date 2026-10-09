---
id: TSO-0155
title: Restore Go 1.27.2 gate compatibility and clear reachable x/net vulnerabilities
status: Done
assignee:
  - '@loop18'
created_date: '2026-10-09 12:45'
updated_date: '2026-10-09 13:15'
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
- [x] #1 Pinned linter parses Go 1.27.2 export data locally and CI uses the matching supported pin without changing gate coverage
- [x] #2 All five module vulnerability scans pass with patched x/net wherever required and tidy is stable
- [x] #3 just check passes on the combined Go-floor prerequisite candidate without gate, assertion or recipe weakening
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Diagnose current upstream compatibility release; update only necessary tool version pins and vulnerable module version/sums, retain gate and test semantics, then security review and sharded CodeRabbit before landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop18: 1 implementation attempt; done after unchanged gate, security review, CodeRabbit and exact-SHA CI. Compatible local linter was installed in an isolated GOBIN for proof.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Aligned the local golangci-lint pin to v2.14.0, already used by CI, and patched root x/net to v0.60.0. No gate or recipe logic changed. Five lint, vulnerability and tidy legs passed; full local and composed gate passed on landed SHA 7fa4fc3f948cfb75fe849052c5b42fc3212ca33e. Security review passed; sharded CodeRabbit completed; exact CI/Helm runs 37934443913 and 37934445673 succeeded. Tests not added for dependency/config updates; existing gate provided validation.
<!-- SECTION:FINAL_SUMMARY:END -->
