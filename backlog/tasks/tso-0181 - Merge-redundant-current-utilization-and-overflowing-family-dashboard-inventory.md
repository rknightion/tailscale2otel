---
id: TSO-0181
title: Merge redundant current-utilization and overflowing-family dashboard inventory
status: To Do
assignee: []
created_date: '2026-10-11 12:09'
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
