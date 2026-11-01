---
id: TSO-0208
title: Unit-test internal/httpretry and merge the duplicate Retry-After parser
status: In Progress
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:01:00Z'
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
- [ ] #1 Direct tests cover delta-seconds and HTTP-date Retry-After, the maxDelay cap, jitter bounds and token-bucket waits under testing/synctest
- [ ] #2 annotations uses the httpretry parser; its behaviour is unchanged under its existing tests
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 retry lane: direct branching tests and parser consolidation, gate and review.
<!-- SECTION:PLAN:END -->
