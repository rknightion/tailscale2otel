---
id: TSO-0186
title: Add retained browser evidence for every dashboard tab and selector state
status: In Progress
assignee: []
created_date: '2026-10-11 12:09'
updated_date: '2026-10-13 21:25'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: task
ordinal: 186000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Both whole-dashboard snapshots succeeded but selected Overview only; 488 panels have no visible render evidence. Query receipts cannot validate transformations, plugins, row gates or actual Grafana ad-hoc filtering. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Drive each of the 22 leaf tabs and capture panel identities, rendered errors, empty states and feature-gate outcomes under a bounded fixture window.
- [ ] #2 Exercise dashboard, tab-local and ad-hoc selectors with non-All values and assert isolation where the signal carries the dimension.
- [ ] #3 Retain screenshots/structured receipts outside tracked source and distinguish dense-render limitations from panel failures.
- [ ] #4 Use a local/fake backend fixture by default; live reads only when explicitly authorized.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner commissioned ordinary work on main on 2026-10-13. Root owns integration, delivery and final proof; bounded workers own disjoint source/evidence paths. Replay remains blocked.
<!-- SECTION:NOTES:END -->
