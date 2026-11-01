---
id: TSO-0202
title: Baseline PAM sessions older than the log horizon on cold start
status: In Progress
assignee:
  - '@loop33'
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:58:00Z'
labels: []
dependencies:
  - TSO-0201
references:
  - internal/collector/pam/sessions.go
priority: medium
type: bug
ordinal: 204000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
With no cursor, internal/collector/pam/sessions.go pages every historical session and emits each as a log stamped Timestamp: s.StartTime, so a cold start or evidence loss with per-session logs enabled sends months-old records the log backend rejects. Same class as TSO-0200. Uses the shared delivery.log_max_record_age key from the horizon-clamp task.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 On a cold start, sessions whose StartTime is older than the horizon are recorded as seen baseline and not emitted; newer sessions are emitted once
- [ ] #2 A warm restart with a cursor emits only sessions after the cursor, unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Use landed delivery.log_max_record_age in PAM sessions: deterministic cold-start older/newer fixtures, seen baseline without old logs, unchanged warm cursor behavior, zero-disable coverage; exact gate and code review.
<!-- SECTION:PLAN:END -->
