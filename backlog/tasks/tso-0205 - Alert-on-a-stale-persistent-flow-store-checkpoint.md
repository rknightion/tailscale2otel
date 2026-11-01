---
id: TSO-0205
title: Alert on a stale persistent flow-store checkpoint
status: Done
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:31:00Z'
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
- [x] #1 A shipped alert fires when the last checkpoint is older than a stated threshold, scoped so deployments without the persistent backend never fire, generated via just gen
- [x] #2 Rule tests cover stale, fresh and absent-series cases
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 checkpoint lane: generated persistent-backend stale checkpoint alert with stale/fresh/absent promtool fixtures.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landed4c84259bf43a5295a69731caf8d25446877454df in tipfdaa4d8b84c44cde5ca99c00f6f4256886a8b409. Exact composed gate20261101T212748Z-tailscale2otel-9e155cc2 exit0 including regeneration/no-diff. Real stale/fresh/absent/recovery fixtures and independent guarded PASS. Default hourly checkpoint compared with2h threshold held15m; longer custom sweep intervals require documented tuning. Live publication separate.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped persistent-store checkpoint-staleness alert with explicit threshold and absent-series suppression.
<!-- SECTION:FINAL_SUMMARY:END -->
