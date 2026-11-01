---
id: TSO-0204
title: Alert on sustained per-entity subrequest coverage below 1
status: In Progress
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:09:00Z'
labels:
  - alerts
dependencies: []
references:
  - internal/apistate/catalog.go
  - deploy/alerts/gen/build_rules.py
priority: medium
type: feature
ordinal: 206000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
tailscale2otel.subrequest.coverage and subrequest.failures (internal/apistate/catalog.go) are visualised but not alertable, so partial per-device posture or invite data degrades silently.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A shipped alert fires when coverage stays below 1 for a sustained window per collector/tailnet and resolves on recovery, generated via just gen
- [ ] #2 Rule tests cover firing, a transient dip that must not fire, and recovery
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 coverage lane: sustained/transient/recovery alert behavior proven using promtool, generated outputs root-owned.
<!-- SECTION:PLAN:END -->
