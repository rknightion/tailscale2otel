---
id: TSO-0088
title: Clarify App.Close flow-store ownership outside Run shutdown
status: Done
assignee: []
created_date: '2026-08-30 12:59'
updated_date: '2026-08-31 03:39'
labels:
  - needs-triage
dependencies: []
priority: low
type: bug
ordinal: 90000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 1 review found that the normal Run shutdown closes per-tailnet flow stores, but App.Close does not. Confirm whether App.Close is a supported lifecycle entry point for tests or embedders. If it is, ensure it cannot leave flow-store workers and databases open; otherwise document and guard the ownership contract.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every supported App lifecycle entry point closes or explicitly transfers ownership of all per-tailnet flow stores
- [x] #2 A focused test or contract guard prevents the non-Run shutdown path from silently leaking flow-store resources
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane I fixes App.Close flow-store ownership outside Run shutdown with a failing lifecycle test first and no public API break.
<!-- SECTION:PLAN:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Made all supported App lifecycle exits close or transfer every per-tailnet flow store and added a focused guard for the non-Run shutdown path. Implementation SHA f35b6ab. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
