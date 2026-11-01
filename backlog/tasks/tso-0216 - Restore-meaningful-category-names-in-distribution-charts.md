---
id: TSO-0216
title: Restore meaningful category names in distribution charts
status: To Do
assignee: []
created_date: '2026-11-01T22:20:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: high
type: bug
ordinal: 218000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Retained live renders label Compliance distribution and Current flow share by transport/traffic type with the generic Value category. Auto-approvers by kind and Auto-approvers (inventory) also lose a category, and Devices matched by integration omits its integration label. The chart cannot communicate its named dimension. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each affected distribution or category chart identifies the state, kind, transport, traffic type or integration represented by every displayed category.
- [ ] #2 A multi-category synthetic frame proves distinct categories remain distinct and named after the actual Grafana transformation pipeline.
- [ ] #3 Before/after renders at normal panel dimensions show the corrected labels without inventing dimensions not emitted by the source.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
