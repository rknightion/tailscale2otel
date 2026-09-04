---
id: TSO-0129
title: Reject coordination timings that client-go cannot run
status: In Progress
assignee:
  - '@codex'
created_date: '2026-09-04 07:02'
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
- [ ] #1 Config validation rejects renew_deadline values that do not exceed the pinned client-go retry jitter bound, with an actionable error
- [ ] #2 Coordinator option validation enforces the same bound so direct callers cannot bypass it
- [ ] #3 Boundary tests cover an invalid jitter-bound configuration and the nearest valid configuration
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add focused failing boundary tests in config and coordination. 2. Align both validation layers with the pinned client-go 1.2 retry jitter requirement and keep existing ordering diagnostics clear. 3. Run focused tests, CodeRabbit, the full gate, and exact-head CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Wave 10 coordination audit found the mismatch at internal/config/validate.go and internal/coordination/coordination.go: repository validation only requires renew_deadline > retry_period, while client-go v0.37.0 rejects renew_deadline <= 1.2 * retry_period.
<!-- SECTION:NOTES:END -->
