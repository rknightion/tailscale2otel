---
id: TSO-0114
title: Reconcile rollback-era legacy checkpoint writes on re-upgrade
status: To Do
assignee: []
created_date: '2026-09-02 15:48'
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
- [ ] #1 The legacy resourceVersion observed at migration is persisted alongside the migrated marker
- [ ] #2 On open, an unchanged legacy resourceVersion skips the merge as today, and a changed one re-merges newest-timestamp-wins
- [ ] #3 A re-merge advances cursors that moved during rollback and never regresses one that the shards already advanced further
- [ ] #4 A re-merge does not resurrect a key the shards deliberately pruned unless the rollback-era release itself still held it
- [ ] #5 A test drives the full migrate, roll back and write, re-upgrade cycle rather than asserting the resourceVersion comparison in isolation
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
