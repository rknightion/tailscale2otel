---
id: TSO-0065
title: Youngest-eviction-age gauge on dedup sets
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:31'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-4
dependencies:
  - TSO-0054
priority: low
ordinal: 68000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The doc-recommended alert ("evictions younger than the overlap horizon" - internal/dedup/dedup.go:105-113) requires correlating two counters and a poll interval by hand. Expose youngest-eviction-age directly per dedup set so the alert is one comparison, catalogue it, and add the generated alert rule. Pairs with the configurable-capacities task TSO-0054.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A youngest-eviction-age gauge exists per dedup set and is catalogued
- [x] #2 A generated alert compares it against the overlap horizon
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Integration deviation: the lane's standalone dedup eviction-age panel contributed to exceeding the frozen 35-panel leaf ceiling. Root folded youngest eviction age into the existing dedup diagnostics panel, preserving visibility and the alert link without raising the ceiling. W1 full gate passed.

Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added youngest-eviction-age telemetry across flow, audit, and routed webhook dedup sets with configured overlap horizons and health-dashboard coverage. Verified by per-set and routed multi-tailnet tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
