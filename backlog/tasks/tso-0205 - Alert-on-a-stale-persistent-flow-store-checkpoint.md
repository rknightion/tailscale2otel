---
id: TSO-0205
title: Alert on a stale persistent flow-store checkpoint
status: In Progress
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:11:00Z'
labels:
  - alerts
dependencies: []
references:
  - internal/appcatalog/catalog.go
priority: low
type: feature
ordinal: 207000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
tailscale2otel.flow_store.last_checkpoint_timestamp (internal/appcatalog/catalog.go) has no rule; a stuck SQLite WAL checkpoint grows the journal unbounded. Only deployments with the persistent backend emit it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A shipped alert fires when the last checkpoint is older than a stated threshold, scoped so deployments without the persistent backend never fire, generated via just gen
- [ ] #2 Rule tests cover stale, fresh and absent-series cases
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 checkpoint lane: generated persistent-backend stale checkpoint alert with stale/fresh/absent promtool fixtures.
<!-- SECTION:PLAN:END -->
