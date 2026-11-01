---
id: TSO-0217
title: Use a comparable scale and explicit measures for observed flow bytes
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
ordinal: 219000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Observed bytes (flow logs) shows generic query-reference labels and radically different bar lengths for comparable displayed byte totals. Independent per-field scaling can make cross-measure comparisons misleading. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The two byte measures have distinct descriptive names.
- [ ] #2 Bar lengths share a meaningful comparable scale for equal-unit values, demonstrated with equal and unequal synthetic totals.
- [ ] #3 The panel documents the aggregation/window and retains correct byte units in a live-style render.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
