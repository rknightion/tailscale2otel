---
id: TSO-0054
title: 'Configurable dedup and seen-set capacities (flow, audit, objectstore)'
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
labels: []
dependencies: []
priority: medium
ordinal: 57000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Three boundary-protection capacities are compile-time constants: flow dedup 16384 (internal/collector/flowlogs/flowlogs.go:42), audit dedup 4096 (internal/collector/auditlogs/auditlogs.go:29, shared default internal/dedup/dedup.go:15), objectstore maxSeenKeys 5000 (internal/collector/objectstore/objectstore.go:100-103). A chatty tailnet can evict entries younger than the overlap horizon the dedup exists to protect, silently double-counting; an objectstore provider writing many small objects inside the lookback re-ingests evicted objects as new. Make all three configurable with the current values as defaults. Pairs with the eviction-age observability task (youngest-eviction gauge).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 All three capacities are config keys with validated bounds and current defaults
- [ ] #2 Docs/schema/env reference regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
