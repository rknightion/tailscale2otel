---
id: TSO-0075
title: Consolidated durable-storage health view on the status page
status: Done
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-5
dependencies: []
priority: medium
ordinal: 78000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Cursor-store vs evidence-store divergence (internal/app/app.go:1194-1275, checkpointReason/evidenceReason) requires correlating two parallel status fields; diagnosing degraded durability means source-diving. Add one "durable state" status panel/section that explains both stores, their modes and the reason for any degradation in operator terms.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 One status-page section presents both stores with mode and degradation reason
- [x] #2 The JSON status API exposes the same consolidated view
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane G adds one consolidated durable-state DTO and status-page section covering cursor and evidence stores, with JSON/HTML tests; it consumes the existing store status rather than introducing another durability model.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane G added one durable_state DTO/API/HTML section covering configured/effective cursor and evidence store modes, path, health state and divergence reason while retaining legacy config fields. Status schema regenerated; JSON/HTML negative guards passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added one consolidated durable-state view for both checkpoint stores, including configured and effective mode, path, state and degradation reason in HTML and JSON. Implementation SHA 6d9c23c. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
