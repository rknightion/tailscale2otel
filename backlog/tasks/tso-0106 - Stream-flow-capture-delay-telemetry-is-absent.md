---
id: TSO-0106
title: Stream flow capture-delay telemetry is absent
status: To Do
assignee: []
created_date: '2026-09-01 18:55'
updated_date: '2026-09-01 20:02'
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
- [ ] #1 An active stream flow feed emits capture-delay histogram samples when the accepted wire record carries both event and capture timestamps
- [ ] #2 Tests cover the live stream-envelope shape and prove capture timestamps reach the accepted-event observer
- [ ] #3 The ingestion dashboard renders flow capture-delay data without changing the documented absent-when-unavailable contract
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Six-hour correlation found stream/flow event-age, accepted-record, and newest-event telemetry continuously present, while stream/flow capture-delay had no series; stream/audit capture-delay was present in the same live build. Resume by reproducing the current publisher envelope shape and tracing its capture timestamp into AcceptedEvent.
<!-- SECTION:NOTES:END -->
