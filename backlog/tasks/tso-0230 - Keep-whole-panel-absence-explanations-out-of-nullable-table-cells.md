---
id: TSO-0230
title: Keep whole-panel absence explanations out of nullable table cells
status: To Do
assignee: []
created_date: '2026-11-01T22:55:00Z'
labels:
  - follow-up
  - dashboards
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: high
type: bug
ordinal: 232000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Connectivity by device shows clipped descriptor or absence text under UDP, and Credential scopes (top-N) injects its panel-wide missing-series explanation into an empty Owner cell even while the row has a numeric measure. A nullable cell is not evidence that the whole query has no data. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Populated rows with nullable metadata or individual measurements use a concise field-appropriate missing state.
- [ ] #2 Whole-query prerequisite explanations are not substituted into arbitrary cells of otherwise populated tables.
- [ ] #3 Synthetic partial-row and wholly empty frames are visually distinguishable and readable at shipped dimensions.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
