---
id: TSO-0045
title: Policy diff log records on revision change
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-2
dependencies:
  - TSO-0044
priority: medium
ordinal: 48000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Companion to the policy-snapshot work (TSO-0044): on ETag/revision change, emit a unified diff between the previous and new policy body as a log record, turning the existing acl.last_changed gauge into an answer to what changed. Requires retaining the prior body across restarts (checkpoint-adjacent, disk-persisted, honouring the same opt-in and PII posture as the snapshots). Depends on the snapshot plumbing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A revision change produces one log record containing a unified diff between old and new policy
- [x] #2 The prior body survives restarts so the first post-restart change still diffs correctly
- [x] #3 Same opt-in flag and PII posture as the snapshot feature
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2 root freeze plan: reuse the ACL snapshot opt-in for policy diffs instead of adding a second flag, keeping one explicit PII consent and avoiding independently enabled snapshot/diff states.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the goal described six hand-maintained config files, but the live TestDocsConfigurationMentionsEveryKey gate proved docs/configuration.md is a seventh required config surface. Added the affected reference entries rather than weakening or bypassing the guard.

Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added revision-driven unified policy diffs over the shared chunk contract with restart-stable baselines and dashboard history. Verified by collector and restart tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
