---
id: TSO-0045
title: Policy diff log records on revision change
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 14:00'
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
- [ ] #1 A revision change produces one log record containing a unified diff between old and new policy
- [ ] #2 The prior body survives restarts so the first post-restart change still diffs correctly
- [ ] #3 Same opt-in flag and PII posture as the snapshot feature
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2 root freeze plan: reuse the ACL snapshot opt-in for policy diffs instead of adding a second flag, keeping one explicit PII consent and avoiding independently enabled snapshot/diff states.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the goal described six hand-maintained config files, but the live TestDocsConfigurationMentionsEveryKey gate proved docs/configuration.md is a seventh required config surface. Added the affected reference entries rather than weakening or bypassing the guard.
<!-- SECTION:NOTES:END -->
