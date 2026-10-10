---
id: TSO-0167
title: >-
  Make Headscale limiter timeout proof deterministic instead of a 5ms network
  race
status: Done
assignee:
  - '@loop20'
created_date: '2026-10-09 22:46'
updated_date: '2026-10-10 00:23'
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
- [x] #1 The limiter wait is proven outside the attempt timeout and request duration without a localhost request completing within a wall-clock margin
- [x] #2 Observed negative checks catch creating the attempt timeout before the limiter wait and charging limiter wait to request duration; production bytes are restored afterward
- [x] #3 Focused race test and just check pass without weakening the contract or widening a timing tolerance
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Use deterministic fake-time/context contract probe instead of wall-clock localhost latency; prove deliberate production ordering regressions fail then restore production bytes, run focused race and serialized full gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
AC2 wording corrected to the behavioral contract: production already starts duration measurement before waiting and subtracts waitDuration. The observed negative mutation removes that exclusion, rather than moving a start that is already early. Timeout-before-wait and wait-charged-duration both fail; no test assertion or production behavior changed.

loop20: 1 implementation attempts; Done after acceptance, landed code and green composed gate. Earlier superseded/cancelled CI runs are not passes.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
loop20 landed evidence 6deb98393a72bda6c030c2c982fc4b5bbf3ea9f9. Replaced flaky localhost5ms margin with real limiter/retry transport and fake network under synctest. Negative timeout-order and wait-charged-duration checks fail; production restored byte-identical. Focused race100 repetitions and full gate green; independent review PASS; completed CodeRabbit zero findings. Final composed just check on actual shipped SHA129945fb3230f1c5d8a1127ad3e3c7faa2c514f4 passed, unchanged tree. Exact CI e96e74e31e2df4c697435dd4b7a32315e0e02f41 run38003933710 ci-success succeeded; no production diff outside backlog from that tested code. Helm absent-not-required (no landed chart change). Just formatting passed. Generated-input DoD is not applicable or covered by gate; no generator drift or weakened test claimed.
<!-- SECTION:FINAL_SUMMARY:END -->
