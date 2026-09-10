---
id: TSO-0148
title: >-
  Decouple ingress WAL completion from metric collection to preserve configured
  DPM
status: To Do
assignee: []
created_date: '2026-09-10 21:02'
updated_date: '2026-09-10 23:10'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 149000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Ingress WAL completion currently calls the shared telemetry Provider.ForceFlush for each accepted streaming or webhook body. This initiates a fresh metric collection outside otlp.metric_interval, exports unrelated metrics sharing the provider, and resets the pinned SDK periodic-reader timer. Incoming traffic can therefore override operator-selected sample cadence and increase datapoints per minute. TSO-0147 mitigated premature flush timeouts but did not correct this contract violation. Revisit the durability and export-scheduling boundary; cadence correctness is primary and throughput is secondary. Cumulative remains the default and the required mode for this work. Delta temporality and loss of metric detail are not acceptable fixes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Metric collection follows the configured schedule: 60s produces the intended 1 DPM and 15s the intended 4 DPM per continuously present series. Streaming arrivals, webhook arrivals, WAL replay and log flushes neither trigger extra collections nor reset that schedule, including for unrelated metrics sharing the provider.
- [ ] #2 Cumulative temporality remains the default and the validated delivery mode. The correction preserves existing metric families and dimensions; switching to delta, reducing detail or enlarging WAL capacity does not satisfy the cadence requirement.
- [ ] #3 WAL entries are committed only after their effects are covered by successfully delivered scheduled metric collections and their required log delivery. Log delivery can progress independently without forcing metrics. Export failure, mixed routes, cancellation and crash replay retain the documented at-least-once durability contract; removing ForceFlush must not permit premature commits.
- [ ] #4 Export retries do not generate newly timestamped collections or out-of-schedule catch-up samples. Collection coverage and acknowledgement semantics are explicit when a scheduled collection is split across requests or when metric and log delivery complete at different times; buffers and backpressure remain bounded.
- [ ] #5 Startup, shutdown, restart and leadership handover have an explicit documented cadence and durability contract with regression coverage. Lifecycle flushing must not silently bypass the configured cadence or discard accepted work; any proposed cadence exception requires an explicit operator decision before implementation.
- [ ] #6 Deterministic regression tests inspect actual exported per-series timestamps and values, including a stable unrelated collector metric, at both 60s and 15s intervals under irregular ingress, bursts, idle periods, backlog replay and exporter failures. Count samples rather than HTTP requests; multiple requests for one scheduled collection are allowed. Establish failing-before and passing-after cadence evidence.
- [ ] #7 Extend the local harness to compare identical admitted WAL entries under sustained arrivals and slow or failing exporters, checking bounded backlog, sample cadence, exported totals and commit safety as well as throughput. Document defaults, delivery-lag tradeoffs and evidence limits; finite coalesced-body results alone do not prove the fix or production capacity.
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

2026-09-11 scope correction requested by the operator: this is a high-priority metric-cadence correctness bug, not a batching enhancement. The revised description and acceptance criteria supersede the earlier throughput-first direction. Historical TSO-0149 measurements remain useful experimental evidence, but its delta case is not an actionable option for this work, and coalescing changes both disk commits and rollup windows. A design must decouple ingress durability completion from scheduled cumulative collection before any throughput claim can close this task. Source pointers: internal/app/ingresswal.go applyEnvelope; internal/app/collectors.go WAL route flush wiring; internal/telemetry/provider.go ForceFlush; internal/telemetry/processors.go newMetricReader. Task remains To Do: this update authorizes no implementation or deployment.
<!-- SECTION:NOTES:END -->
