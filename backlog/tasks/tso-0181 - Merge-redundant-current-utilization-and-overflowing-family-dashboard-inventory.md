---
id: TSO-0181
title: Merge redundant current-utilization and overflowing-family dashboard inventory
status: Parked
assignee: []
created_date: '2026-10-11 12:09'
updated_date: '2026-10-11 18:22'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 181000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The current utilization table and bargauge execute the same expression; overflow inventories are distributed across multiple rows answering the same affected-family question. History and discrepant overflow evidence are still actionable and should be retained. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 One current-utilization presentation gives affected metric family and configured-limit fraction, alongside the retained history view.
- [ ] #2 One diagnostic row reconciles self-reported overflow flags with overflow-label series and identifies affected families.
- [ ] #3 Every currently covered catalog signal retains panel coverage after consolidation; record query/panel count reduction.
- [ ] #4 Keep native fallback navigation if an optional visualization plugin is missing.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.
<!-- SECTION:NOTES:END -->
