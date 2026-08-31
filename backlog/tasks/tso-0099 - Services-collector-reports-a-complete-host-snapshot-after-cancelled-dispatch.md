---
id: TSO-0099
title: Services collector reports a complete host snapshot after cancelled dispatch
status: To Do
assignee: []
created_date: '2026-08-31 10:55'
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
- [ ] #1 fetchHosts returns an explicit completion state and Collect emits the host snapshot only when dispatch completed
- [ ] #2 A regression test cancels after one job completes but before the remainder are dispatched, and asserts no snapshot is emitted
- [ ] #3 Per-result observation replaces the aggregate record, or the duplicate is shown not to occur
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
