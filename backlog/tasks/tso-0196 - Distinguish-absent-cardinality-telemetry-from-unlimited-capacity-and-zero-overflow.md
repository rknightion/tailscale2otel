---
id: TSO-0196
title: >-
  Distinguish absent cardinality telemetry from unlimited capacity and zero
  overflow
status: Done
assignee:
  - '@loop31'
created_date: '2026-10-12 22:17'
updated_date: '2026-10-13 13:00'
labels:
  - needs-triage
dependencies: []
priority: medium
type: bug
ordinal: 196000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Guarded description review found existing cardinality empty-state copy still asserts unlimited on a missing limit series, while an overflow summary zero-fills missing reporter data. The configured positive limit gauge is intentionally absent in unlimited mode but can also be absent when self-observability is unavailable. Prose-only corrections do not change these value mappings or query fallbacks. This needs a separate source-presence fix rather than silently expanding the frozen description task.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Absent relevant reporter telemetry produces an explicit missing or uncertain state, not an asserted unlimited capacity or healthy zero overflow.
- [x] #2 Observed finite-limit and genuinely unlimited configurations preserve correct interpretation with selected scope.
- [x] #3 Real query fixtures and rendered evidence discriminate missing telemetry from observed zero/unlimited cases without weakening existing contracts.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: inspect shipped producer presence/limit/overflow evidence, encode honest missing/observed state without new producer signal; actual-query/render cases for missing, mixed reporters, finite,zero overflow, indistinguishable unlimited and selected scope; named gate, root guarded review/integration/CI/delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop31: 1 implementation attempt; no source edits during proof continuations. Missing capacity cannot establish unlimited; unmatched visible partial/mixed reporters remain uncertain; absent overflow no longer healthy-zero fallback, observed finite/zero results restricted to observed subset. Seven actual real-producer/Prometheus/Grafana cases and real datasource provider filtering with positive raw controls pass. Ignored proof locator/fixture-UID repairs and initial startup readiness failure retained without weakening assertions. Green candidate gate needed one parallel-lint infrastructure retry; exact fresh composed0ee91ed681dfaa07ca92bec6f9f9cbb6322e13d0 passes. Full-hunk guarded review PASS; CodeRabbit staged-Python No files defect explicitly unreviewed, not clean. Published exact source CI38398607897 ci-success, sync38398608067 and downstream workflows success; far-side health48d732b3e641d7f9630fc1d1b1ad00a948e77737 matches. Helm absent by paths. Residuals: fully unseen reporters do not establish coverage, heterogeneous finite max/max arithmetic frozen, prior green default styling retained for explicit unknown text, optional treemap runtime untested. Full gate stdout tail-only. Attempts1; complete.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Removed absent-to-zero overflow fallbacks and added honest uncertain-capacity guards/copy using shipped observations only. Missing, partial, mixed, finite, zero and indistinguishable unlimited query/render cases verified with real selected scope. Independently reviewed, exact composed/CI/publication green, GitSync source blobs match.
<!-- SECTION:FINAL_SUMMARY:END -->
