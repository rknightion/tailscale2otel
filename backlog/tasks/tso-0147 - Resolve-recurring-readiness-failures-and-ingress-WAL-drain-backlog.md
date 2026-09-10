---
id: TSO-0147
title: Resolve recurring readiness failures and ingress WAL drain backlog
status: In Progress
assignee:
  - '@codex'
created_date: '2026-09-10 19:55'
updated_date: '2026-09-10 20:18'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 148000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Recurring readiness 503s persist beyond the bounded promotion drain. Live evidence shows near-capacity ingress WAL and network log delivery refusals. Diagnose replay throughput and transient readiness state without weakening durability or deleting accepted records.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Identify supported causes of recurring readiness failures and sustained WAL backlog, distinguishing observed evidence from hypotheses
- [x] #2 Implement and verify the bounded correction supported by the investigation, preserving accepted-record durability
- [ ] #3 Report deployment identity and remaining live verification boundaries
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Capture live readiness reasons, WAL state and safe storage/export diagnostics. 2. Trace the deployed replay and append paths and reproduce the supported defect. 3. Implement the smallest durable correction and run regression checks and review. 4. Verify delivery and report any remaining operational work.

Bounded correction: raise the whole-ingress-flush budget from 10s to 60s, preserving the failure-retention and shutdown-cancellation contracts. Add deterministic coverage of a healthy serial-batch flush longer than 10s. This is a timeout correction, not a claim that per-envelope cumulative exports scale indefinitely.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Live authenticated component state repeatedly observed ingress_wal retrying. Goroutine snapshots show applyEnvelope waiting in Provider.ForceFlush and SDK PeriodicReader.ForceFlush while the reader continues successful metric batch exports. Metrics exporter has zero failures during the captured window. Live scrape has approximately 144000 raw flow counter series; batch size is 10000 datapoints and observed requests take roughly 0.5-0.7 seconds. The fixed 10-second whole-flush wait competes with serial batches and collection time. WAL pending bytes subsequently fell to about 17 MB, so capacity alone does not explain recurring retrying.

Regression TestIngressWALCoordinator_CommitsAfterHealthyBatchedFlush fails against the original 10s budget with ingress WAL flush unavailable after 10 successful batches, and passes with the 60s whole-flush budget. Existing bounded-failure and flush/commit retry tests pass. Live configuration enables both raw and rollup flow metrics, external addresses uncollapsed, destination-port, identity and geo dimensions; raw byte and packet counters each had about 72000 series in the captured scrape. This explains the large cumulative collection; changing that policy would remove raw metric dimensions and is not part of the timeout correction.

A direct 30-sample live readiness capture reproduced alternating 200 ok and 503 ingress_wal: retrying on the active leader, beyond startup. CodeRabbit directory-sharded review completed with zero findings and reviewed both changed source files. No config changes, WAL deletion, node changes or telemetry-detail reductions were made.

Deployed OCI index digest 812b11f50ce35181a07ed1609c077c9237e5ce6d32c14c24aefec36405f277f4 resolves to source revision 36583713c1f41e7d6efe040737a2b62440ec2e7d. Its WAL coordinator, readiness handler, provider flush, batch processor and WAL storage source are unchanged at the pre-fix checkout HEAD. The published version string is 6.0.0-rc.1 although the binary uses the v5 module path; deployment identity was checked by digest and source, not semver ordering.

The first full gate passed formatting, all module lints and vet, then found three shutdown-budget contract failures. The tests had coupled the runtime per-entry flush constant to shutdown, whereas production actually bounded the final WAL drain using the telemetry shutdown timeout. Split out an explicit 10s ingressWALDrainTimeout and wired both production and contract arithmetic to it. The running flush gets 60s; deployment grace remains unchanged at 55s. A second final review covers this wiring correction.

Final just check exited 0 after separating the runtime and shutdown budgets. All five modules passed vulnerability checks; the root and tool-module race suites passed. Final CodeRabbit review completed with zero findings across all five changed Go files. No generated artifact inputs changed semantically; drift checks passed. Next boundary: commit/push, exact-source CI and automated RC publication, then tag-only GitOps rollout and live readiness/backlog read-back.
<!-- SECTION:NOTES:END -->
