---
id: TSO-0208
title: Unit-test internal/httpretry and merge the duplicate Retry-After parser
status: Done
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:17:00Z'
labels:
  - test
dependencies: []
priority: low
type: chore
ordinal: 210000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/httpretry (token bucket, jittered backoff, Retry-After parsing) has no _test.go and is exercised only through tsapi/hsapi. internal/annotations/client.go carries a near-duplicate parseRetryAfter.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Direct tests cover delta-seconds and HTTP-date Retry-After, the maxDelay cap, jitter bounds and token-bucket waits under testing/synctest
- [x] #2 annotations uses the httpretry parser; its behaviour is unchanged under its existing tests
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 retry lane: direct branching tests and parser consolidation, gate and review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landed8ccbe4c10d10d78b3ed07952a8edc57723909ce1 in composed tip1646978409c509a5eedaf1ac8f4bab1e9db75b1f. Exact composed gate run20261101T211407Z-tailscale2otel-cb40d4c5 exit0. Shared parser preserves annotation trim/immediate/cap policy and fixes overflow extremes. Direct synctest token bucket/jitter/delay/cancellation proofs, CodeRabbit complete and final dsreview PASS.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Consolidated Retry-After parsing with overflow-safe conversion and direct deterministic retry tests; exact composed gate passed.
<!-- SECTION:FINAL_SUMMARY:END -->
