---
id: TSO-0101
title: Reject an already-cancelled request before it consumes an admission slot
status: In Progress
assignee: []
created_date: '2026-08-31 10:55'
updated_date: '2026-09-01 18:36'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: low
ordinal: 102000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/stream/stream.go around lines 767-768 takes a slot from the admit channel without first checking ctx.Err(). A client that has already gone away therefore consumes admission capacity and proceeds into body processing before anything notices. Minor in isolation, but it interacts with TSO-0059 per-tailnet admission fairness: a route whose clients disconnect under load spends its sub-budget on requests that can never be answered, which is precisely the starvation that task exists to prevent. Check ctx.Err() before acquiring, preserving the existing release callback and success path. Found by the post-Wave-3 sharded CodeRabbit pass.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An already-cancelled request is rejected without consuming an admission slot, proven by a test
- [ ] #2 The release callback and the success path for live requests are unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
- Write a failing admission test proving an already-cancelled request consumes a slot.
- Reject cancelled requests before slot acquisition while preserving race-safe cancellation.
- Run targeted tests and return changed paths plus evidence without committing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
- Admission rejects an already-cancelled context before slot acquisition and rechecks cancellation immediately after either successful channel send, releasing an acquired slot before returning.
- Deterministic regressions failed before the fix for both immediate and timed send branches with `ok=true`, then passed with the slot still available.
- Final CodeRabbit stream shard completed with 0 findings; the combined focused race gate for stream, services and webhook passed.
<!-- SECTION:NOTES:END -->
