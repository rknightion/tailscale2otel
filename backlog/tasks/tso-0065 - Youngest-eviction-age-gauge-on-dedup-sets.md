---
id: TSO-0065
title: Youngest-eviction-age gauge on dedup sets
status: To Do
assignee: []
created_date: '2026-08-30 09:31'
labels: []
dependencies: []
priority: low
ordinal: 68000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The doc-recommended alert ("evictions younger than the overlap horizon" - internal/dedup/dedup.go:105-113) requires correlating two counters and a poll interval by hand. Expose youngest-eviction-age directly per dedup set so the alert is one comparison, catalogue it, and add the generated alert rule. Pairs with the configurable-capacities task TSO-0054.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A youngest-eviction-age gauge exists per dedup set and is catalogued
- [ ] #2 A generated alert compares it against the overlap horizon
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
