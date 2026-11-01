---
id: TSO-0227
title: Name used scrape budget consistently with the displayed fraction
status: To Do
assignee: []
created_date: '2026-11-01T22:55:00Z'
labels:
  - follow-up
  - dashboards
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: low
type: bug
ordinal: 229000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Scrape budget headroom computes scrape duration divided by interval, which is the used fraction; the description and high-is-bad thresholds also describe usage. The title presents the opposite quantity. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The title, description, expression and thresholds all describe used budget or all consistently describe remaining headroom.
- [ ] #2 A fixture using one quarter of the interval cannot be described as having only one quarter remaining.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
