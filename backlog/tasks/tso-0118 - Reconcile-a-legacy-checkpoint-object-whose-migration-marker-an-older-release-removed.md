---
id: TSO-0118
title: >-
  Reconcile a legacy checkpoint object whose migration marker an older release
  removed
status: To Do
assignee: []
created_date: '2026-09-03 19:35'
labels:
  - needs-triage
dependencies: []
priority: high
type: bug
ordinal: 119000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TSO-0114 shipped newest-wins reconciliation for a legacy checkpoint ConfigMap that a rolled-back release reopened and advanced. The Wave 8 live cycle proved it does not fire in the real rollback shape, because the older release removes the migration marker when it writes.

Live evidence (Wave 8, TSO-0115). A sharded release migrated two shards and marked the legacy object. A rollback to the pre-sharding release acquired the Lease, reopened the retained legacy object, advanced both polling cursors, changed its resourceVersion and grew it to 1299 bytes. Re-upgrading restored the marker and advanced the shard migration baselines, but did not merge the newer cursors: the audit cursor stayed about 29 seconds behind legacy and flowlogs about 81 seconds behind. Those are re-polled windows on every rollback round trip.

Diagnosis, from the code at 33ff841. NewKubernetesCheckpointStore in internal/collector/checkpoint_kubernetes.go selects its merge semantics on legacy.LegacyMigrated alone. The pre-sharding release does not preserve the marker, so a re-upgrade after a rollback takes the FIRST branch, which is the interrupted-migration path and merges only keys absent from the shards (if _, exists := state.m[key]; exists { continue }). The newest-wins branch is unreachable in exactly the case it was written for. The store already holds a durable signal the marker cannot give it: legacyMigrationBaseline() returns a resourceVersion only when every shard agrees, which is true only after a migration completed. A complete baseline plus a missing marker means an older client rewrote the object, not that migration was interrupted.

The generalizable defect is that a marker stored on a shared object is not a durable signal when another release can rewrite that object. The durable signal has to live where only the current release writes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A legacy checkpoint object that carries no migration marker but whose shards all carry a matching migration baseline is reconciled newest-wins, not merged missing-only
- [ ] #2 A genuinely interrupted multi-shard migration, where no complete baseline exists, still takes the missing-only path and cannot lose a shard's newer row
- [ ] #3 A failing-first test reproduces the live shape: migrate, strip the marker while advancing two cursors in the legacy object, reopen, and assert both cursors advanced in the shards
- [ ] #4 A rollback round trip is idempotent: reopening again with no further legacy write performs no shard write
- [ ] #5 Whether the re-upgrade restores the marker on the legacy object is decided explicitly and recorded, since no current release reads it once a baseline exists
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
