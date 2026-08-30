---
id: TSO-0064
title: rdns warm-start snapshot across restarts
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:31'
updated_date: '2026-08-30 16:32'
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
- [x] #1 After a restart, previously cached PTR names serve immediately within their TTLs
- [x] #2 Snapshot is bounded, best-effort (corrupt/missing snapshot = clean cold start), and covered by tests
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2 Rule Zero choice: use a deterministic checkpoint-adjacent RDNS snapshot path instead of adding a configurable key. This is the narrowest reversible option and avoids a config key that might survive unwired.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added deterministic checkpoint-adjacent RDNS warm-start snapshots for unexpired positive PTR names only; negative cache entries remain memory-only. Verified by restart, expiry, corruption, and negative-entry tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
