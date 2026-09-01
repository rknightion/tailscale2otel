---
id: TSO-0110
title: Support high-cardinality replay state in Kubernetes checkpoint storage
status: To Do
assignee: []
created_date: '2026-09-01 21:38'
labels:
  - needs-triage
dependencies: []
priority: medium
type: enhancement
ordinal: 111000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Kubernetes checkpoint backend stores the whole shared checkpoint map in one ConfigMap. Ordinary poll cursors are tiny, but configured worst-case replay identities and object-store seen-key state can exceed the 1,048,576-byte ConfigMap data limit (measured synthetic bounds: 15,859,713 and 1,680,001 bytes). The Wave 5 backend rejects oversize maps visibly and never truncates; define a scalable persistence contract before coordinated deployments rely on those high-cardinality features.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Configured replay and object-store checkpoint state persists without exceeding the selected Kubernetes object limits
- [ ] #2 A deposed leader cannot overwrite current state and oversize handling never truncates or silently drops keys
- [ ] #3 Tests cover ordinary poll cursors and configured high-cardinality replay and object-store bounds
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
