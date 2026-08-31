---
id: TSO-0076
title: Debounce checkpoint writes across collectors
status: In Progress
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-31 02:13'
labels: []
milestone: m-6
dependencies: []
priority: medium
ordinal: 79000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Every checkpoint Set re-marshals and double-fsyncs the entire shared JSON file (internal/collector/checkpoint.go:178-183, 223-229); dozens of tailnets x window collectors approaches a full-file fsync per second, painful on NFS/EFS-backed volumes. Add a short debounce/coalesce window collapsing same-instant ticks into one write, without weakening the durability contract for shutdown or the finalize-in-one-call semantics. Note the HA design (TSO-0033) may introduce a kubernetes checkpoint backend - keep the seam compatible.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Concurrent Sets within the debounce window produce one persisted write
- [ ] #2 Shutdown still flushes synchronously; crash-window loss is bounded and documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes checkpoint write debounce with zero preserving synchronous writes; lane C later implements coalescing and shutdown flush.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane C added file-store write debounce/coalescing with zero preserving synchronous behavior, retained the option after corrupt-file recovery, and wired synchronous cursor/evidence Flush into Run and Close shutdown paths. Concurrent-set and root shutdown-persistence tests passed.
<!-- SECTION:NOTES:END -->
