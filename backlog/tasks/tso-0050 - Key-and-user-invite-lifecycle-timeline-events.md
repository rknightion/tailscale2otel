---
id: TSO-0050
title: Key and user-invite lifecycle timeline events
status: To Do
assignee: []
created_date: '2026-08-30 09:27'
labels: []
dependencies: []
priority: low
ordinal: 53000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Keys and user invites carry created/expiry/updated timestamps; emit normalized lifecycle events (key created/revoked/expiring-soon, invite created/accepted) as log records to drive a timeline panel. Overlaps audit logs - the value is the always-on normalized shape independent of audit-log availability/retention. Coordinate the expiring-soon event with the key-expiry log-mode rework (candidate C1) so the two do not produce duplicate noise.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Lifecycle transitions for keys and invites are emitted as normalized structured log records
- [ ] #2 A timeline-style dashboard panel is fed by them
- [ ] #3 No duplicate noise against the reworked key-expiry warning mode
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
