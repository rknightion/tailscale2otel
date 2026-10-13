---
id: TSO-0184
title: >-
  Emit tailnet ownership on runtime traces before offering tailnet-scoped trace
  panels
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
ordinal: 184000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
app.New passes a.tracer (process tracer) to runtime clients/schedulers and receivers. These spans do not carry a runtime tailnet dimension; adding a query matcher would silently hide valid spans. Only the emitted scrape collector dimension is repaired in this candidate. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Two-runtime local trace-export fixture proves scheduler/API/node/receiver spans carry the correct tailnet ownership where applicable.
- [ ] #2 Process-global release-check spans remain explicitly process-global.
- [ ] #3 Trace panels honor a non-All tailnet selection without mixing runtime ownership or erasing valid process traces under All.
- [ ] #4 Root verifies both All and selected-tailnet TraceQL against bounded live evidence after delivery.
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
