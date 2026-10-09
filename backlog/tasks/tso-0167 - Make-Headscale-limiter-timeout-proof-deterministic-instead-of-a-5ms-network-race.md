---
id: TSO-0167
title: >-
  Make Headscale limiter timeout proof deterministic instead of a 5ms network
  race
status: In Progress
assignee:
  - '@loop20'
created_date: '2026-10-09 22:46'
updated_date: '2026-10-09 22:47'
labels: []
dependencies: []
references:
  - internal/hsapi/retry_test.go
priority: high
type: bug
ordinal: 167000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Loop20 local gate failed in TestClientRateLimitWaitIsOutsideAttemptTimeoutAndRequestDuration on an unchanged Python-only candidate. The existing localhost HTTP request must finish inside 5ms and compares elapsed wall-clock margins. Retrying past the scheduling flake is not proof; the limiter-wait-versus-attempt-timeout contract needs a deterministic observation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The limiter wait is proven outside the attempt timeout and request duration without a localhost request completing within a wall-clock margin
- [ ] #2 The test catches moving the attempt timeout or duration start before the limiter wait, with an observed negative check
- [ ] #3 Focused race test and just check pass without weakening the contract or widening a timing tolerance
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Use deterministic fake-time/context contract probe instead of wall-clock localhost latency; prove deliberate production ordering regressions fail then restore production bytes, run focused race and serialized full gate.
<!-- SECTION:PLAN:END -->
