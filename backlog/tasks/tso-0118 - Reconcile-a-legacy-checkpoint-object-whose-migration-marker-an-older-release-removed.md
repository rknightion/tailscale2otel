---
id: TSO-0118
title: >-
  Reconcile a legacy checkpoint object whose migration marker an older release
  removed
status: Parked
assignee: []
created_date: '2026-09-03 19:35'
updated_date: '2026-09-03 21:07'
labels: []
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
- [x] #1 A legacy checkpoint object that carries no migration marker but whose shards all carry a matching migration baseline is reconciled newest-wins, not merged missing-only
- [x] #2 A genuinely interrupted multi-shard migration, where no complete baseline exists, still takes the missing-only path and cannot lose a shard's newer row
- [x] #3 A failing-first test reproduces the live shape: migrate, strip the marker while advancing two cursors in the legacy object, reopen, and assert both cursors advanced in the shards
- [x] #4 A rollback round trip is idempotent: reopening again with no further legacy write performs no shard write
- [x] #5 Whether the re-upgrade restores the marker on the legacy object is decided explicitly and recorded, since no current release reads it once a baseline exists
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add a failing-first Kubernetes checkpoint test that performs the complete migration, simulates a pre-sharding writer by removing the marker and advancing two legacy cursors, then reopens and proves the current missing-only branch loses those writes.
2. Select reconciliation semantics from the shard-owned complete migration baseline while preserving missing-only behavior for genuinely incomplete migration, explicitly decide marker restoration, and keep the round trip idempotent.
3. Run the focused collector tests, inspect the diff, and return the decision and evidence to the root for integration and tracker finalization.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implementation 0513e636: changed markerless reopen selection to use the complete shard-owned migration baseline as the durable completed-migration signal. A complete matching baseline now selects newest-wins even when a pre-sharding writer removed the legacy marker; a genuinely incomplete migration with no complete baseline remains missing-only. The re-upgrade deliberately does not restore the legacy marker because no current release needs it once the shard baseline exists, and restoring shared-object metadata would add writes without correctness value. The failing-first writer-shaped test migrated, removed the marker, advanced both legacy cursors, and failed under the old code with the audit cursor still at 12:00 instead of 12:01. Focused collector tests, idempotent reopen with no shard write, exact-commit build, regeneration drift check, and full just check passed. CodeRabbit completed for internal/collector; two major findings asserting /v4 were shard-context false positives against the verified /v5 tree.

Wave integration stop: pushed code head b1ed782322fc66cc9c14a5a6be09d00fe3071c68 passed exact-head CI run 33805396385 attempt 1, but release-please updated PR #585 while leaving it open at 4.1.0. The pushed breaking header is malformed as feat!(config): rather than the Conventional Commits form feat(config)!:, so the breaking change was not classified. Goal section 8 requires an immediate stop on non-retarget and forbids forcing release configuration. Phase 3 was therefore not started and no lab object was created or mutated. Resume only after an owner-approved, non-history-rewriting metadata correction makes PR #585 target 5.0.0; then obtain the immutable final-head image, run the full direct-endpoint rollback cycle, record both cursor pairs and idempotency, and delete/read-back every temporary object before moving this task to Done.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implementation is complete at 0513e636 and all task acceptance criteria and local gates are proven, but the task is Parked at the campaign hard stop because the mandatory Wave 9 live rollback cycle could not start before PR #585 retargeted to 5.0.0. No lab mutation occurred.
<!-- SECTION:FINAL_SUMMARY:END -->
