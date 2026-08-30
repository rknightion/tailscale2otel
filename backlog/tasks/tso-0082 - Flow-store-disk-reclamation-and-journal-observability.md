---
id: TSO-0082
title: Flow store disk reclamation and journal observability
status: In Progress
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-30 23:58'
labels: []
milestone: m-6
dependencies: []
priority: medium
ordinal: 85000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
sweep() deletes rows (internal/flowstore/sqlitestore/writer.go:202-226) but no auto_vacuum/incremental vacuum is configured (schema.go:198-200), so the .db never shrinks after lowering retention or max_rows; and Stats() (writer.go:231-286) omits journal-file size and last-checkpoint time, which disk alerts need. Add an incremental-vacuum tick, the two stats fields (catalogued + status page), and optionally an admin-triggered manual vacuum.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Lowering retention eventually reduces on-disk size without manual intervention
- [ ] #2 Journal size and last-checkpoint time are exported and visible
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes incremental-vacuum interval/pages with disabled defaults; lane I later implements reclamation, stats telemetry, and panel.

Lane I must wire incremental vacuum into the SQLite store so the zero interval selects an automatic cadence derived from the existing sweep interval, while a positive interval overrides it; the page limit bounds each tick. Verify retention reduction reclaims pages without an admin action.

F1 wording correction: the frozen default is an automatic-cadence default. incremental_vacuum_interval=0 inherits flows.store.sweep_interval and enables automatic reclamation; positive values override that cadence.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
F1 clarification after review: incremental_vacuum_interval=0 is not disabled; it selects automatic reclamation on the existing sweep cadence. Positive values are explicit cadence overrides, and incremental_vacuum_pages bounds each tick. This supersedes the stray phrase 'disabled defaults' in the appended plan.

CodeRabbit's implementation finding was accepted for wording and resolved append-only; lane I owns the behavior.
<!-- SECTION:NOTES:END -->
