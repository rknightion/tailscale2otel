---
id: TSO-0082
title: Flow store disk reclamation and journal observability
status: Done
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-31 03:39'
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
- [x] #1 Lowering retention eventually reduces on-disk size without manual intervention
- [x] #2 Journal size and last-checkpoint time are exported and visible
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes incremental-vacuum interval/pages with disabled defaults; lane I later implements reclamation, stats telemetry, and panel.

Lane I must wire incremental vacuum into the SQLite store so the zero interval selects an automatic cadence derived from the existing sweep interval, while a positive interval overrides it; the page limit bounds each tick. Verify retention reduction reclaims pages without an admin action.

F1 wording correction: the frozen default is an automatic-cadence default. incremental_vacuum_interval=0 inherits flows.store.sweep_interval and enables automatic reclamation; positive values override that cadence.

Lane I consumes the frozen automatic-cadence incremental-vacuum settings, implements reclamation and journal/checkpoint observability with TDD, and adds the assigned ingestion panel.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
F1 clarification after review: incremental_vacuum_interval=0 is not disabled; it selects automatic reclamation on the existing sweep cadence. Positive values are explicit cadence overrides, and incremental_vacuum_pages bounds each tick. This supersedes the stray phrase 'disabled defaults' in the appended plan.

CodeRabbit's implementation finding was accepted for wording and resolved append-only; lane I owns the behavior.

CodeRabbit found the one-time full VACUUM conversion shared the normal 15s query budget. Root separated it behind an internal five-minute startup conversion budget, keeping the public config seam unchanged; ordinary admin queries remain bounded by flows.store.query_timeout.

Negative guard evidence for the conversion budget: root deliberately switched the full VACUUM back to the ordinary query context; TestOpen_ConvertsExistingDatabaseOutsideQueryTimeout failed with context deadline exceeded. Restoring the dedicated conversion context made the same test pass.

Second CodeRabbit review found passive checkpoint completion was inferred from busy=0 alone. Root now scans busy/log/checkpointed frame counts and records last-checkpoint time only when all WAL frames were checkpointed; table tests cover complete, empty, busy, partial and unavailable-count results.

Third CodeRabbit pass completed with no critical or major findings. Its one minor was accepted: the reclamation test now requires a passive WAL checkpoint with busy=0 and equal log/checkpointed frame counts before taking the database-file size baseline.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added automatic flow-store disk reclamation when retention falls, plus journal size and last-checkpoint observability surfaced in telemetry and the status UI. Implementation SHA f35b6ab. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
