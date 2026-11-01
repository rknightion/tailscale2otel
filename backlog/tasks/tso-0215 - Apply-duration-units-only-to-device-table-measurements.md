---
id: TSO-0215
title: Keep metadata free of numeric and duration formatting
status: To Do
assignee: []
created_date: '2026-11-01T22:06:00Z'
updated_date: '2026-11-01T22:20:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_inventory.py
priority: medium
type: bug
ordinal: 217000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Last seen (time since) visibly formats an OS-version metadata cell with a seconds suffix in the retained live Inventory and Hygiene render. A panel-wide duration unit must not alter metadata presentation. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 fix set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Only the intended age/duration measurement fields receive duration units in device tables.
- [ ] #2 OS versions and other metadata preserve their original semantic display, including numeric-looking strings.
- [ ] #3 A synthetic frame containing numeric-looking metadata and a real duration catches the previous formatting error.
- [ ] #4 Identifier metadata remains recoverable without large-number abbreviation or numeric coercion that changes its meaning.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Node Metrics tables also abbreviate identifier metadata with large-number suffixes. Preserve identifiers and numeric-looking metadata semantically, in addition to restricting duration units to real measurements.
<!-- SECTION:NOTES:END -->
