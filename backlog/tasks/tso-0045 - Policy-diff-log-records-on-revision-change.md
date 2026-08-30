---
id: TSO-0045
title: Policy diff log records on revision change
status: To Do
assignee: []
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 09:28'
labels: []
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
