---
id: TSO-0064
title: rdns warm-start snapshot across restarts
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-30 09:31'
updated_date: '2026-08-30 13:48'
labels: []
milestone: m-4
dependencies: []
priority: medium
ordinal: 67000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/rdns cold-starts on every restart, flapping flow endpoint labels to external until PTRs re-resolve - the exact flap the stale-TTL engineering (#297) exists to prevent. Persist a small bounded addr/name/expiry snapshot beside the checkpoint file, loaded on startup, honouring existing TTL semantics (expired entries not resurrected).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 After a restart, previously cached PTR names serve immediately within their TTLs
- [ ] #2 Snapshot is bounded, best-effort (corrupt/missing snapshot = clean cold start), and covered by tests
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2 Rule Zero choice: use a deterministic checkpoint-adjacent RDNS snapshot path instead of adding a configurable key. This is the narrowest reversible option and avoids a config key that might survive unwired.
<!-- SECTION:PLAN:END -->
