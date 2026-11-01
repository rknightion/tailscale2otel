---
id: TSO-0223
title: Use the correct ratio scale for delivery failure charts
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
ordinal: 225000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Delivery failure ratio by type has an axis reaching ten thousand percent, compressing the meaningful fraction range. The configured unit and numeric bounds must use one convention. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A normal fraction fixture renders a meaningful zero-to-one-hundred-percent scale and readable intermediate failure ratios.
- [ ] #2 Unit, bounds and threshold configuration agree on the input convention.
- [ ] #3 A source-based or rendered negative reproduction catches the previous inflated axis.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
