---
id: TSO-0187
title: Make automatic rule publication explicit in dashboard-only loop authority
status: Done
assignee: []
created_date: '2026-10-11 12:09'
updated_date: '2026-10-11 15:18'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: task
ordinal: 187000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The existing grafana-sync workflow deliberately prunes and pushes rules on every dashboard-path push. A campaign that authorizes GitSync dashboard delivery while forbidding these side effects cannot safely publish. Do not silently change this established delivery behavior; the owner must choose an explicit automation grant or a separately approved workflow change. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Repository loop preparation documentation explicitly enumerates the automatic rule prune/push on dashboard-only delivery.
- [x] #2 A dashboard-write campaign records an owner-approved choice covering the existing side effects or separately authorizing a delivery-behavior change, before publication.
- [ ] #3 Any separately approved workflow change preserves the existing authorized rule-delivery path and is covered by failing-before/passing-after workflow-contract proof.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Closed at loop27 preparation (2026-10-11, Rob's choice). AC #1: LOOP.md Traps now states that any deploy/grafana/** push runs grafana-sync's unconditional rules job (prune, then gcx push), and that a goal granting dashboard delivery grants that rule publication too; verified against grafana-sync.yml (the rules job has no if/needs). AC #2: loop27's goal records owner decision D57 authorising those side effects before any publication. AC #3: not applicable, since no workflow change was approved or made. DoD: docs-only LOOP.md change; loop-lint loopmd exit 0; just check not run (LOOP.md is not a gate input). CodeRabbit skipped (docs).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Made automatic rule publication explicit: a LOOP.md trap plus an owner decision in loop27's goal. No workflow change.
<!-- SECTION:FINAL_SUMMARY:END -->
