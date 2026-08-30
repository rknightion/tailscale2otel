---
id: TSO-0047
title: Device inventory change-log to Loki
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 14:00'
labels: []
milestone: m-2
dependencies: []
priority: medium
ordinal: 50000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Emit a per-device change record (name, os, client version, tags, routes, key-expiry deltas; device added/removed) whenever the devices poll observes a difference, answering "when did this device change" without audit-log archaeology. The enrich/devices path already sees successive polls; needs a bounded prior-state snapshot (disk-persisted like checkpoints, or accept per-restart re-baseline without emitting a storm on startup). Cardinality-safe by construction (logs, not series). Respect pii_filter for names/users.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Field-level device changes and add/remove events are emitted as structured log records
- [ ] #2 Process restart does not emit a spurious change-storm
- [ ] #3 PII-bearing fields honour the existing pii_filter controls
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2 root freeze plan: add the devices change-log opt-in default off; lane implementation must consume it or the key will be reverted at run end.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the goal described six hand-maintained config files, but the live TestDocsConfigurationMentionsEveryKey gate proved docs/configuration.md is a seventh required config surface. Added the affected reference entries rather than weakening or bypassing the guard.
<!-- SECTION:NOTES:END -->
