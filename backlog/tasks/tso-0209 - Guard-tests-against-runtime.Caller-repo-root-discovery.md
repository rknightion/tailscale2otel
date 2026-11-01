---
id: TSO-0209
title: Guard tests against runtime.Caller repo-root discovery
status: To Do
assignee: []
created_date: '2026-11-01T20:55:00Z'
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
- [ ] #1 A test fails, naming the file, when any _test.go outside internal/repotest calls runtime.Caller to locate the repository; proven by a planted negative
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
