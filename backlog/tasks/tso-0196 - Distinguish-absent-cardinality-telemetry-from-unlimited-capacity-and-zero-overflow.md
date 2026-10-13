---
id: TSO-0196
title: >-
  Distinguish absent cardinality telemetry from unlimited capacity and zero
  overflow
status: In Progress
assignee:
  - '@loop31'
created_date: '2026-10-12 22:17'
updated_date: '2026-10-13 10:53'
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
- [ ] #1 Absent relevant reporter telemetry produces an explicit missing or uncertain state, not an asserted unlimited capacity or healthy zero overflow.
- [ ] #2 Observed finite-limit and genuinely unlimited configurations preserve correct interpretation with selected scope.
- [ ] #3 Real query fixtures and rendered evidence discriminate missing telemetry from observed zero/unlimited cases without weakening existing contracts.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: inspect shipped producer presence/limit/overflow evidence, encode honest missing/observed state without new producer signal; actual-query/render cases for missing, mixed reporters, finite,zero overflow, indistinguishable unlimited and selected scope; named gate, root guarded review/integration/CI/delivery.
<!-- SECTION:PLAN:END -->
