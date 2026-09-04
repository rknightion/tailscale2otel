---
id: TSO-0129
title: Reject coordination timings that client-go cannot run
status: Done
assignee:
  - '@codex'
created_date: '2026-09-04 07:02'
updated_date: '2026-09-04 07:11'
labels:
  - needs-triage
dependencies: []
priority: high
type: bug
ordinal: 130000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Configuration validation currently accepts renew_deadline values greater than retry_period, but the pinned client-go leader elector requires renew_deadline to exceed retry_period multiplied by its 1.2 jitter factor. A configuration such as 15s, 11s, 10s passes config validation and then fails during coordinator construction, turning a recoverable configuration error into a runtime startup failure.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Config validation rejects renew_deadline values that do not exceed the pinned client-go retry jitter bound, with an actionable error
- [x] #2 Coordinator option validation enforces the same bound so direct callers cannot bypass it
- [x] #3 Boundary tests cover an invalid jitter-bound configuration and the nearest valid configuration
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add focused failing boundary tests in config and coordination. 2. Align both validation layers with the pinned client-go 1.2 retry jitter requirement and keep existing ordering diagnostics clear. 3. Run focused tests, CodeRabbit, the full gate, and exact-head CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Wave 10 coordination audit found the mismatch at internal/config/validate.go and internal/coordination/coordination.go: repository validation only requires renew_deadline > retry_period, while client-go v0.37.0 rejects renew_deadline <= 1.2 * retry_period.

Red proof: the focused config and coordinator tests both failed because the 12s renew deadline with a 10s retry period was accepted. Green proof: both layers now reject equality with the client-go 1.2 jitter bound and accept 12s plus 1ns; focused tests passed. CodeRabbit completed over all four changed internal files with zero findings. just check passed at commit 0e212ab5. The pre-commit hook regenerated the affected metrics and root config-schema families and found both already up to date.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Aligned configuration and coordinator validation with client-go JitterFactor 1.2 so invalid election timings fail early with an actionable error. Boundary tests prove equality is rejected and the nearest representable duration above it is accepted; review and the full gate pass.
<!-- SECTION:FINAL_SUMMARY:END -->
