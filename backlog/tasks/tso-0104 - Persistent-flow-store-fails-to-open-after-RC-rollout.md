---
id: TSO-0104
title: Persistent flow store fails to open after RC rollout
status: To Do
assignee: []
created_date: '2026-09-01 17:45'
updated_date: '2026-09-01 20:02'
labels: []
milestone: m-10
dependencies: []
priority: high
type: bug
ordinal: 105000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live validation of the current release-candidate image found the configured persistent flow store failing during startup while converting its database to incremental auto-vacuum. The exporter stayed ready, but the flow explorer was disabled and both flow-store observability metrics were absent. The validation lane records evidence only; diagnose and repair this separately without changing live data until the on-disk state and rollback path are understood.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A regression test reproduces the observed startup failure or proves the exact environmental precondition that caused it
- [ ] #2 The persistent flow store opens without data loss across the affected upgrade path
- [ ] #3 The flow explorer remains enabled and the journal-size and last-checkpoint metrics are present after restart
- [ ] #4 A live lab upgrade and rollback read-back prove the database remains usable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The lab was restored to the known-good pre-Wave-3 digest after evidence capture. Live build-info read-back reported 4.1.0-rc.29 and startup produced no flow-store, disk-I/O, error, or fatal log. No repair was attempted in the validation lane.

Pre-wave triage on main (hypothesis, NOT yet proven against the lab file): the failing path is configureIncrementalAutoVacuum in internal/flowstore/sqlitestore/schema.go:275-306, called unconditionally from openDB at schema.go:227. A database created before incremental auto-vacuum shipped carries auto_vacuum=NONE; PRAGMA auto_vacuum=2 does not take effect until a full VACUUM rewrites the file, so startup runs VACUUM bounded by opts.ConversionTimeout, default 5 minutes (store.go:63-66,153-154). Any failure there returns an error from the store open, which is why the exporter stayed ready while the flow explorer was disabled and both flow-store metrics were absent - the store never opened.

Two candidate preconditions to reproduce, both consistent with the observed symptom and neither yet confirmed: (a) the VACUUM exceeded 5 minutes on the lab's accumulated flow history; (b) VACUUM ran out of space - it rewrites the whole database into a temp file, so it needs roughly 2x the database size free on the same filesystem plus SQLite temp space. Capture the pod's actual database size and filesystem free space before theorising further; the distinction changes the fix (raise/removed timeout and make it resumable, versus preflight the free space and degrade instead of failing the open).

Design question the fix has to answer either way: a one-time storage optimisation should probably not be able to take the flow store down. Consider failing open - keep the store usable in the old auto-vacuum mode, emit a self-obs signal, and retry the conversion later - rather than returning an error from the open path.
<!-- SECTION:NOTES:END -->
