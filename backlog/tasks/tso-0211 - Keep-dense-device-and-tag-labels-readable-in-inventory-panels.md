---
id: TSO-0211
title: Keep dense entity and category labels readable in dashboard panels
status: To Do
assignee: []
created_date: '2026-11-01T22:06:00Z'
updated_date: '2026-11-01T23:32:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_inventory.py
priority: high
type: bug
ordinal: 213000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A retained live Inventory and Hygiene render shows heavy label overlap in Device online state by node and Fleet tags (rollup), preventing individual rows or categories from being identified. Query success does not establish readable presentation. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 fix set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A dense synthetic fleet and tag fixture renders readable displayed row/category labels at the shipped panel dimensions without overlap.
- [ ] #2 Any bounded or scrollable presentation preserves the stated population and makes each displayed identifier/category recoverable.
- [ ] #3 Retained before/after renders cover both affected panels and their normal dashboard placement.
- [ ] #4 The quantity-defining title of ACL change evidence age (summary) remains fully readable or explicitly recoverable at its normal shipped dimensions.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Additional retained renders show the same label-readability pattern in Service tags (rollup), Encryption posture population and Route-selection mode. Preserve recoverable identities/categories rather than silently narrowing the displayed population.

Additional exact audit binding: ACL change evidence age (summary) truncates the title defining its duration measurement at the shipped width. Keep the full quantity meaning recoverable alongside the readable value; fold this title-legibility case into the same text layout contract.
<!-- SECTION:NOTES:END -->
