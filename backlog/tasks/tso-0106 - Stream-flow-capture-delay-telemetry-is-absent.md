---
id: TSO-0106
title: Stream flow capture-delay telemetry is absent
status: Done
assignee: []
created_date: '2026-09-01 18:55'
updated_date: '2026-09-01 23:37'
labels: []
milestone: m-10
dependencies: []
priority: medium
type: bug
ordinal: 107000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live Wave 3 validation found event-age and accepted-record telemetry for stream flow records, but no capture-delay histogram series for the same active source/signal. Audit stream capture delay is present. Without the flow series, operators cannot distinguish upstream capture lag from delivery/backfill lag when flow event age rises.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An active stream flow feed emits capture-delay histogram samples when the accepted wire record carries both event and capture timestamps
- [x] #2 Tests cover the live stream-envelope shape and prove capture timestamps reach the accepted-event observer
- [x] #3 The ingestion dashboard renders flow capture-delay data without changing the documented absent-when-unavailable contract
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Reproduce the live stream-envelope capture timestamp loss with a failing telemetry test; restore capture-delay observation; specify the required root-owned dashboard panel; validate the focused stream path.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Six-hour correlation found stream/flow event-age, accepted-record, and newest-event telemetry continuously present, while stream/flow capture-delay had no series; stream/audit capture-delay was present in the same live build. Resume by reproducing the current publisher envelope shape and tracing its capture timestamp into AcceptedEvent.

Live before/after evidence is decisive: rc.29 emitted stream/flow event-age and accepted-record series but no stream/flow capture-delay series. After rc.52, the active stream feed emitted 900 flow capture-delay samples in the final verification process. The ingestion dashboard was published through GitSync and its far-side blob hash matched dad5a85dc6dd4e60aee438c1989054af1039dd90.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Restored live stream-envelope capture timestamps in f825e5d and added the root-owned ingestion panel at integrated head 48bf65c8bf30c0f77f679728b4b56947bd5df944. Telemetry/WAL tests cover accepted and replayed live shapes plus the absent-when-unavailable case. Live rc.52 samples, GitSync far-side proof, generated-artifact gates, full just check, and exact-head CI 33569379997 passed.
<!-- SECTION:FINAL_SUMMARY:END -->
