---
id: TSO-0147
title: Resolve recurring readiness failures and ingress WAL drain backlog
status: Done
assignee:
  - '@codex'
created_date: '2026-09-10 19:55'
updated_date: '2026-09-10 20:59'
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
- [x] #3 Report deployment identity and remaining live verification boundaries
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

Source fix committed as 423c3ae40ce7b436c952b1f20b294d054bdba828. Exact-source CI run 34525608587 is active; automated RC publication must succeed before rollout. Deployment uses the infrastructure repository image tag override via Argo. Preserve the existing unrelated infrastructure tracker edit; only the exporter image tag is in rollout scope.

Exact-source CI 34525608587 completed successfully with every job successful at 423c3ae40ce7b436c952b1f20b294d054bdba828. Automated RC run 34526425328 is now publishing that source. Independent CodeQL, Docker Security, actionlint, zizmor and Scorecard runs also succeeded.

Automated prerelease v5.0.3-rc.14 resolves through its annotated tag to source 423c3ae40ce7b436c952b1f20b294d054bdba828. Infrastructure tag-only change is prepared and just check passes after restoring two missing pinned local provider packages without AWS backend access or lockfile changes. Existing infrastructure kubeconform gate skipped 129 unrelated CRD resources; this is not all-schema proof. Separate Helm rendering confirms two replicas, persistent claim template, unchanged 55s termination grace and the selected image. Image publication remains pending; no rollout yet.

Container and chart publication jobs succeeded in RC run 34526425328. Published image index 037eedfaf39bb8572ea62b032bf95350bc9c369cae90e6bffa047eaa5130e775 has source revision 423c3ae40ce7b436c952b1f20b294d054bdba828 on both platforms. Found an auto-regression risk: the updater selected registry tags by semver and the old higher-numbered image has no GitHub release. Changed only this image updater to the GitHub releases datasource with v-prefix extraction, preserving RC automerge policy. Renovate JSON schema and extraction checks passed; infrastructure just check passed again; the changed rendered StatefulSet passed schema validation with zero skips. Declarative config changes did not require new tests or CodeRabbit. GitOps commit ee3979c is the deployment/updater correction; live rollout read-back remains pending.

GitOps rollout completed: both replicas run published image 5.0.3-rc.14, digest 037eedfaf39bb8572ea62b032bf95350bc9c369cae90e6bffa047eaa5130e775, from source 423c3ae40ce7b436c952b1f20b294d054bdba828. Release run 34526425328 completed successfully including binaries; its redundant wait-for-ci job was intentionally skipped after the successful triggering CI. Five-minute live readiness sample: 150 of 150 HTTP 200, stable pod identity and zero restarts. One earlier post-promotion readiness 503 event remains recorded, alongside an initial log-export failure that subsequently recovered; no claim of zero lifetime failures. Four actual configuration-log requests delivered eight records and WAL pending entries/bytes returned to zero. Network-flow arrivals remain absent: read-only upstream status reports context deadline exceeded and an old last-success time, while configuration deliveries succeed at the identical destination URL. Startup WAL was already empty, so this rollout does not prove drainage of the old backlog or sustained high-cardinality throughput. Remaining boundary is restoring upstream network delivery without unrequested tailnet configuration mutation, then observing WAL flush completion under real flow load.

User explicitly authorized changes to the upstream network-stream configuration. The exporter OAuth identity rejected PUT with 403; user then supplied the location of existing admin credentials. Reapplied only the existing network destination using its unchanged URL, compression and current receiver token. API PUT returned 200; read-back confirmed network configuration preserved and audit configuration unchanged. No delete, ACL change or credential scope expansion. Actual network flow delivery resumed immediately: more than 25000 records processed in the first observation window. Catch-up backlog rose to about 48 MB; metric export failures remained zero and no additional Kubernetes readiness events appeared. Monitoring loaded WAL drainage before declaring recovery.

Loaded recovery observed after authorized network-stream reapply: 55765 flow records processed; WAL pending bytes fell from about 48 MB to 27 MB and then zero, with zero pending entries. Upstream delivery status recovered with an empty lastError and successful fresh requests. Metric exporter reached 377 exports with zero failures; the only log-export failure predates stream recovery and subsequent exports succeeded. Both pod UIDs remained stable with zero restarts. Kubernetes Unhealthy history remains the single earlier post-promotion event, with no additions during catch-up. The concurrent local port-forward readiness sampler had three-second transport timeouts while large metrics scrapes used the same forward; those samples are not a pass and their cause is not proven.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Resolved and deployed the premature whole-metric-flush timeout in 5.0.3-rc.14 (source 423c3ae40ce7b436c952b1f20b294d054bdba828), with failing-before/passing-after regression coverage, full just check, zero-finding final CodeRabbit review, exact-source CI and automated release proof. With explicit user authorization, reapplied only the existing upstream network-stream configuration using existing admin credentials; network and audit configuration read-back was unchanged. Authentic flow delivery resumed, 55765 records were processed and catch-up WAL backlog drained from approximately 48 MB to zero. Both deployed pod identities stayed stable with zero restarts and no new Kubernetes readiness failure events. The loaded local port-forward sample returned 139 HTTP 200 and 11 transport timeouts, not a perfect probe pass; no sampled HTTP 503. This is bounded recovery evidence, not indefinite capacity or zero-loss proof. No WAL deletion, telemetry dimension reduction or ACL change. No new application code changed during stream recovery, so previous source gates were not repeated; live read-back validates this operational change.
<!-- SECTION:FINAL_SUMMARY:END -->
