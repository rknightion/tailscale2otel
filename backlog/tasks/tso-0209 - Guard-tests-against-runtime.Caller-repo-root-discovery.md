---
id: TSO-0209
title: Guard tests against runtime.Caller repo-root discovery
status: Done
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:12:00Z'
labels:
  - test
dependencies: []
priority: low
type: chore
ordinal: 211000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Commit 55c8d1d9 replaced runtime.Caller root discovery with internal/repotest.Root so tests pass under -trimpath. Nothing stops a new test reintroducing it. The gate recipes stay unchanged (Standing forbids gate edits).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A test fails, naming the file, when any _test.go outside internal/repotest calls runtime.Caller to locate the repository; proven by a planted negative
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 rootguard lane: AST guard with planted negative, gate and review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landed 3d9e40b0a70e8d9abd4933621858bf9fc33385d9 after composed remote gate run 20261101T210854Z-tailscale2otel-8528c5f7 exit0, including just check, generation idempotence and just formatting. Planted negative catches file/line; alias/dot imports covered. CodeRabbit complete0, final dsreview PASS. CI watched separately.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added AST regression guard against trimpath-unsafe runtime.Caller use in repository tests; planted negative and clean trimpath suite passed, then exact composed gate and main push.
<!-- SECTION:FINAL_SUMMARY:END -->
