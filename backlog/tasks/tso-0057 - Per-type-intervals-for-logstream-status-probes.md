---
id: TSO-0057
title: Per-type intervals for logstream status probes
status: Done
assignee: []
created_date: '2026-08-30 09:30'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-6
dependencies: []
priority: low
ordinal: 60000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Both the configuration and network log-stream status probes share one 600s cadence (internal/collector/logstream/logstream.go:141-166). An operator caring only about flow delivery health pays for both at the same rate. Allow per-type intervals (defaulting to the shared value).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Configuration and network probes can run on independent intervals
- [x] #2 Existing single-interval configs keep working unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes per-type log-stream probe intervals inheriting the existing shared 10m default; lane C later wires independent scheduling.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane C implemented independent configuration/network log-stream probe intervals with zero inheriting the shared interval. The registry now schedules at the faster effective cadence while each probe observes its own due time; focused race tests passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added independent configuration and network probe intervals for logstream status collection while retaining compatibility with the existing single interval. Implementation SHA 6d9c23c. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
