---
id: TSO-0229
title: Name active-series cap utilization as a ratio rather than a count
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
ordinal: 231000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Active series over time divides the series population by its limit and displays a percentage. Its title implies an absolute series count, so the question, units and expression disagree. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The shipped title and legend explicitly identify cap utilization for the existing fraction expression.
- [ ] #2 A known count and limit render the correct utilization with percentage units.
- [ ] #3 Unknown, unlimited and observed-reporter qualification from the existing cap contract remain intact.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
