---
id: TSO-0090
title: >-
  just verify-deploy reports unreachable when any unrelated gcx context is
  broken
status: In Progress
assignee: []
created_date: '2026-08-30 18:31'
updated_date: '2026-08-31 03:12'
labels:
  - needs-triage
milestone: m-5
dependencies: []
priority: medium
ordinal: 91000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
scripts/verify_deployment.py runs `gcx config check`, which reports on EVERY configured context, and treats a non-zero exit as "gcx cannot reach Grafana (not logged in, or offline)" — exit code 2, unreachable. Observed 2026-08-30: the m7kni context was online (Grafana 13.3.0) and had just accepted a 126-resource rule push, but two sibling contexts hold keychain-locked credentials, so config check exited non-zero and verify-deploy declared the whole stack unreachable. The repo drift gate therefore returns "unreachable" on a healthy stack whenever an unrelated context is stale, which is the normal state on a machine with several stacks configured. Scope the reachability check to the context the push actually targets.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 verify-deploy's reachability check considers only the context it is verifying against, not every configured gcx context
- [ ] #2 A broken sibling context is proven not to change the exit code (negative test: break a sibling, confirm exit stays 0 on a healthy target)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane K scopes verify-deploy reachability to the selected target context and negative-tests that a broken sibling context cannot change the healthy target exit.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane K scoped reachability to gcx config check --context <current>. The red-first negative test proves an unscoped broken sibling cannot affect a healthy selected context; Python tests passed and a live read-only scoped check reported the selected target online.

Required CodeRabbit pre-commit review attempted on the integrated staged diff after just check passed; the service failed before analysis with recoverable  and emitted no  line. Treated as a failed review, not a clean result. Root manually reviewed the full staged diff and found no blocking issue; this is an overnight review-service deviation.

Correction to the preceding note: the exact recoverable error was WebSocket closed, and the review emitted no complete status line.
<!-- SECTION:NOTES:END -->
