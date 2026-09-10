---
id: TSO-0148
title: Batch ingress WAL flush barriers to improve sustained replay throughput
status: To Do
assignee: []
created_date: '2026-09-10 21:02'
updated_date: '2026-09-10 21:18'
labels: []
dependencies: []
priority: high
type: enhancement
ordinal: 149000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TSO-0147 corrected premature 10s flush timeouts, but each accepted receiver body still forces a complete cumulative metric collection. At high cardinality, repeated collections can constrain drain capacity even when exports succeed. Reduce this amplification without changing accepted-record durability or hiding backpressure.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Multiple eligible WAL entries share bounded export barriers with defined entry, byte and latency limits
- [ ] #2 Failures, cancellation, mixed routes and crash replay retain at-least-once durability without committing unflushed entries
- [ ] #3 Reproducible local load evidence compares throughput, backlog, export amplification and relevant settings against the per-entry baseline
- [ ] #4 Defaults and tuning tradeoffs are documented; no production rollout is inferred from local results
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
TSO-0149 supplies the local comparison harness. Its coalesced-body experiment reduces both flush barriers and disk commits and changes the rollup top-N window; it is directional evidence, not proof of a multi-entry group-commit implementation. Compare identical admitted entries when implementing this task.

Local harness delivered in TSO-0149. Two workload sizes each ran all six cases twice with exact exported byte accounting and zero pending WAL state. On the larger local case, fewer-barrier coalescing drained in about 2s versus about 15s baseline; delta about 2.45s; external collapsing about 1.6s; rollup-only about 8s; smaller OTLP batches 40-47s. These are synthetic finite-drain results with documented confounders, not production sizing or implemented multi-entry batching. See docs/wal-load-testing.md.
<!-- SECTION:NOTES:END -->
