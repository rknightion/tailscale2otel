---
id: TSO-0088
title: Clarify App.Close flow-store ownership outside Run shutdown
status: To Do
assignee: []
created_date: '2026-08-30 12:59'
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
- [ ] #1 Every supported App lifecycle entry point closes or explicitly transfers ownership of all per-tailnet flow stores
- [ ] #2 A focused test or contract guard prevents the non-Run shutdown path from silently leaking flow-store resources
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
