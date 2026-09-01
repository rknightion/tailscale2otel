---
id: TSO-0104
title: Persistent flow store fails to open after RC rollout
status: To Do
assignee: []
created_date: '2026-09-01 17:45'
updated_date: '2026-09-01 19:09'
labels:
  - needs-triage
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
<!-- SECTION:NOTES:END -->
