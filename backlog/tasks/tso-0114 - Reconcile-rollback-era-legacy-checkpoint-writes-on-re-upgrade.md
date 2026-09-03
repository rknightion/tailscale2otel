---
id: TSO-0114
title: Reconcile rollback-era legacy checkpoint writes on re-upgrade
status: Done
assignee:
  - '@codex'
created_date: '2026-09-02 15:48'
updated_date: '2026-09-03 13:57'
labels: []
dependencies:
  - TSO-0110
priority: medium
type: bug
ordinal: 115000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Migration to sharded checkpoints marks the legacy ConfigMap migrated and deliberately retains its JSON so a rollback to the single-ConfigMap release can still reopen its state. That retention creates a gap in the other direction. If an operator rolls back, the old release keeps advancing cursors in the legacy object, knowing nothing about shards or the marker. On re-upgrade, NewKubernetesCheckpointStore sees LegacyMigrated set and skips the merge entirely, so every cursor advance made during the rollback is discarded and the shards resume from where migration left them.

The consequence is duplicate emission across the whole rollback window, not data loss, and the window is as long as the rollback lasted. Lane D found this in Wave 6 and left it as an owner-policy question.

Owner decision 2026-09-02: re-merge only when the legacy object has actually changed. Record the legacy resourceVersion at migration; on open, compare it and merge newest-timestamp-wins if it differs. Cursors are monotonic, so newest-wins is safe.

Rejected, with reasons: merging unconditionally on every open resurrects seen-keys the shards have since pruned, leaking state back into exactly what sharding bounds; deleting the legacy object removes the ambiguity by removing the rollback path, so an older release would start empty and fully replay.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The legacy resourceVersion observed at migration is persisted alongside the migrated marker
- [x] #2 On open, an unchanged legacy resourceVersion skips the merge as today, and a changed one re-merges newest-timestamp-wins
- [x] #3 A re-merge advances cursors that moved during rollback and never regresses one that the shards already advanced further
- [x] #4 A re-merge does not resurrect a key the shards deliberately pruned unless the rollback-era release itself still held it
- [x] #5 A test drives the full migrate, roll back and write, re-upgrade cycle rather than asserting the resourceVersion comparison in isolation
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 8 Lane B: test-first, persist the legacy resourceVersion observed during migration; on reopen skip an unchanged legacy object and newest-timestamp-wins re-merge only a changed one; drive the full migrate, rollback-write, and re-upgrade cycle including cursor non-regression and pruned-key behavior; return focused race-test evidence without committing or pushing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Wave 8 Lane B implemented rollback-era reconciliation in local commit 47f16b8. The adapter returns the post-marker legacy resourceVersion and shards persist it as migration metadata; unchanged baselines skip, while changed/absent/inconsistent baselines conservatively re-merge newest-timestamp-wins and repair metadata. The full-cycle failing-first test initially left the rollback cursor at 12:00 instead of 12:01, then focused race tests passed for internal/collector and internal/coordination. Empty legacy state creates no permanent metadata-only object and safely re-inspects until rows exist. Two directory-sharded CodeRabbit reviews completed; their cross-directory missing-type/interface findings were false positives against the combined four-file seam. just build passed before commit. The initial push was rejected because origin/main concurrently advanced to the independent shared-workflow v1.18.1 repin; integration is deferred until active lanes finish, without rebase/reset/stash.

Final integration at 1c088cea1dbdd9fbcd0d59086953bada2a9ff69f: just check passed; just gen left no diff; just --fmt --check passed; exact-head CI 33762639276 succeeded on attempt 1. CodeRabbit completed; its directory-scoped missing-type findings were checked against the combined seam and were false positives.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Commit 47f16b8 persists the post-migration legacy resourceVersion on every shard and conditionally reconciles a changed legacy object newest-timestamp-wins. The failing-first full migrate, rollback-write, re-upgrade test proved advances, non-regression, and the intentional retained-key behavior; focused race tests and the integrated gate passed.
<!-- SECTION:FINAL_SUMMARY:END -->
