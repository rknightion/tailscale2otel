---
id: TSO-0050
title: Key and user-invite lifecycle timeline events
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-2
dependencies:
  - TSO-0051
priority: low
ordinal: 53000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Keys and user invites carry created/expiry/updated timestamps; emit normalized lifecycle events (key created/revoked/expiring-soon, invite created/accepted) as log records to drive a timeline panel. Overlaps audit logs - the value is the always-on normalized shape independent of audit-log availability/retention. Coordinate the expiring-soon event with the key-expiry log-mode rework (TSO-0051, a dependency) so the two do not produce duplicate noise.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Lifecycle transitions for keys and invites are emitted as normalized structured log records
- [x] #2 A timeline-style dashboard panel is fed by them
- [x] #3 No duplicate noise against the reworked key-expiry warning mode
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Wave 2 Rule Zero decision: the current invite API exposes only open invites and no created, updated, accepted, revoked, or cancelled timestamps. Implement the narrow truthful reversible shape: tailscale.user_invite.observed on first observation and tailscale.user_invite.no_longer_open on disappearance, with transition=observed|no_longer_open. Never infer acceptance or another terminal cause from absence. This owner-unavailable choice must appear in the run-end Questions for the human section.

Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added key lifecycle events plus truthful invite observed/no_longer_open transitions without inferring acceptance, avoiding expiry-log duplication and feeding the timeline surface. Verified by lifecycle telemetry tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
