---
id: TSO-0099
title: Services collector reports a complete host snapshot after cancelled dispatch
status: Done
assignee: []
created_date: '2026-08-31 10:55'
updated_date: '2026-09-01 19:10'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: medium
ordinal: 100000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Two findings from the post-Wave-3 sharded CodeRabbit pass, both in internal/collector/services/services.go and both about TSO-0037 work:

1. Around line 218 the worker loop defers apistate.Observe until after the loop, aggregating results, where observing each API result as it returns would avoid the duplicate record the aggregate produces.
2. Around lines 226-229 fetchHosts does not distinguish "all service requests completed" from "cancelled partway through dispatch". Collect then emits docHostInfo from a partial result as though it were a full snapshot, so a cancellation during dispatch silently publishes an incomplete host inventory that looks authoritative.

The second is the one that matters: a host snapshot that is quietly partial is worse than one that is absent, because nothing downstream can tell. Have fetchHosts return an explicit completion state and skip the snapshot when it is incomplete, with a regression test covering cancellation after one job completes but before the rest are dispatched.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 fetchHosts returns an explicit completion state and Collect emits the host snapshot only when dispatch completed
- [x] #2 A regression test cancels after one job completes but before the remainder are dispatched, and asserts no snapshot is emitted
- [x] #3 Per-result observation replaces the aggregate record, or the duplicate is shown not to occur
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
- Reproduce the cancelled-dispatch partial host snapshot through emitted telemetry.
- Fix the services collector to publish only a complete snapshot.
- Run targeted tests and return changed paths plus evidence without committing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
- `fetchHosts` now reports whether dispatch completed; a cancelled partial host enumeration is discarded rather than emitted as a complete `host.info` snapshot.
- The telemetry regression failed before the fix by emitting one partial snapshot, then passed with no snapshot emitted.
- Replaced a `runtime.Gosched` hint with explicit cancellation-observation synchronization after CodeRabbit flagged nondeterminism; repeated the regression 20 times under race.
- Final CodeRabbit services shard completed with 0 findings.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Services host collection now returns an explicit dispatch-complete state and suppresses cancelled partial snapshots. The telemetry regression failed before the fix, passed after it, and remained stable across 20 race runs; per-result observation remains nonduplicating.
<!-- SECTION:FINAL_SUMMARY:END -->
