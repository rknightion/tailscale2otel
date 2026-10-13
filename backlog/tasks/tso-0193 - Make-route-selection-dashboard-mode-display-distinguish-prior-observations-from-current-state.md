---
id: TSO-0193
title: >-
  Make route-selection dashboard mode display distinguish prior observations
  from current state
status: In Progress
assignee:
  - '@loop31'
created_date: '2026-10-12 20:40'
updated_date: '2026-10-13 10:53'
labels:
  - needs-triage
dependencies: []
priority: low
type: bug
ordinal: 193000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The route-selection info gauge clears obsolete SDK series after a successful poll, but its generated table uses the established two-hour configuration evidence window. A mode change can leave prior and current stored modes visible together, and loss of mode support can show an earlier observation. This is a display limitation, not an alert or control-plane change. Fresh review established it from the generated expression; no live instance values are required.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The route-mode display either selects the latest applicable observation or explicitly distinguishes prior observations from current state.
- [ ] #2 Actual query fixtures cover a mode change and absent current mode without erasing useful history or selected tailnet/provider scope.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: retain scoped two-hour query/history/title, label observed route modes explicitly historical; reproduce actual mode-change/absent-current fixtures, portable tests and named gate, then root guarded review/integration/CI/delivery.
<!-- SECTION:PLAN:END -->
