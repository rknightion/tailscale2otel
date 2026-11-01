---
id: TSO-0221
title: Make topology panels interpretable in populated and empty states
status: To Do
assignee: []
created_date: '2026-11-01T22:20:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 223000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Traffic topology - ROLLUP uses very thin dark connections, while VIP service topology is entirely blank without a visible explanation. The blank capture alone does not establish whether the source is empty or the plugin is failing. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Populated topology relationships have sufficient contrast and readable connection structure at shipped dimensions.
- [ ] #2 Legitimate empty or unavailable topology states provide an actionable visible explanation.
- [ ] #3 The VIP panel source/plugin prerequisite is adjudicated and a nonempty synthetic frame proves supported rendering.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
