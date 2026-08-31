---
id: TSO-0056
title: Scale the scheduler stagger window with deployment size
status: In Progress
assignee: []
created_date: '2026-08-30 09:30'
updated_date: '2026-08-31 02:13'
labels: []
milestone: m-6
dependencies: []
priority: low
ordinal: 59000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
One fixed 3s defaultStaggerWindow (internal/collector/scheduler.go:128-136) is shared by every tailnet runtime scheduler; an MSP with dozens of tailnets gets a first-tick thundering herd it cannot widen without a code change. Scale the window with tailnet/collector count (or make it configurable) while keeping the single-tailnet behaviour unchanged by default.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 First-tick spread grows with the number of scheduled units, or is operator-configurable
- [ ] #2 Single-tailnet default behaviour unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes an operator-configurable scheduler stagger window preserving the current 3s default; lane C later wires and tests it.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane C wired scheduler.initial_stagger_window into every runtime scheduler. The frozen 3s default preserves single-tailnet behavior while operators can widen the first-tick spread for larger deployments; scheduler guard tests passed.
<!-- SECTION:NOTES:END -->
