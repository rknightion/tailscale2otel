---
id: TSO-0082
title: Flow store disk reclamation and journal observability
status: To Do
assignee: []
created_date: '2026-08-30 09:35'
labels: []
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
