---
id: TSO-0047
title: Device inventory change-log to Loki
status: To Do
assignee: []
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 09:47'
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
