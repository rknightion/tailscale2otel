---
id: TSO-0132
title: >-
  Prove the coordinated Kubernetes surface live: standby scrapes, demotion,
  Lease loss, clock skew
status: Done
assignee:
  - '@codex'
created_date: '2026-09-04 07:31'
updated_date: '2026-09-04 12:28'
labels:
  - needs-triage
dependencies: []
priority: high
type: chore
ordinal: 133000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 10 changed what a standby replica serves on its Prometheus endpoint and proved it with unit tests alone. No lab cycle ran. Wave 8 is the precedent that makes this worth doing: every offline test there passed against a defect the first live rollback cycle found in one pass, because each of those tests modelled the older release as a reader when the real one was a writer.

What is unexercised against a real cluster, all of it introduced or changed since the last live cycle:

- A standby's /metrics endpoint. The gatherer is selected per scrape rather than at listener start, so a demoted leader is supposed to drop its collector series on the very next scrape. Nothing has scraped a real standby.
- Demotion itself. A former leader must retain process telemetry and lose the full gatherer, with no window where stale tailnet series are still scrapeable.
- A scrape landing concurrently with a leadership transition. The implementation notes that a gather already in progress may return the prior surface; that boundary has never been hit for real.
- Lease deletion and conflicting replacement beneath a live leader, which is TSO-0130's subject.
- API-server loss during renewal, and clock skew against the renew deadline. Wave 10's audit reasoned about both and found no defect, but reasoning is not observation.
- The three Wave 10 alert rules have never been watched through a complete evaluation window.

Also verify one shipped behaviour that looks like a defect on paper: CoordinationNoStandby fires below one standby, while the chart defaults replicaCount to 1 and coordination.mode is set independently. A single-replica coordinated deployment therefore alerts permanently. Decide whether that is correct advisory behaviour, a chart validation gap, or a rule that should exclude single-replica deployments.

Lab constraints. Isolated sibling deployment only, never the managed workload, its Deployment or its PVC. Every temporary object deleted and read back absent by exact name before the run ends. RBAC proofs go through the direct cluster endpoint: the tailnet API proxy answers auth can-i --as as the operator, so a negative test through it returns a false yes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A real standby is scraped and serves process telemetry including its coordination state, with no collector or per-tailnet series present
- [x] #2 A leader is demoted and the next scrape after demotion carries the standby surface, with the observed lag recorded
- [x] #3 Lease deletion and conflicting replacement are exercised against a live leader and the observed duplicate-active window is measured, feeding TSO-0130
- [x] #4 API-server unavailability during renewal and a configured clock skew are exercised, and the behaviour is recorded whether or not it is a defect
- [x] #5 The three coordination rules are observed through at least one full evaluation window and their firing history is recorded
- [x] #6 The single-replica CoordinationNoStandby behaviour is adjudicated and the verdict recorded
- [x] #7 The lab is returned to its prior configuration and image, and every temporary object is confirmed absent by exact name
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Snapshot the isolated lab deployment and prove direct-cluster RBAC before mutation.
2. On the current image, measure standby scrape contents, demotion-to-standby scrape lag, Lease-deletion duplicate-active window, API-server renewal loss, clock skew, and one complete alert evaluation window. Record sanitized observations as they are taken.
3. Integrate the commissioned fencing, adjudication, counter, dashboard, and alert work; run generation and the repository gate.
4. Repeat the same Lease-deletion harness against the fix, observe rule history, then restore the exact prior lab image/configuration and prove every temporary object absent by exact name.
5. Record source, CI, deployment, live proof, skips, and residual unknowns separately.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Single-replica adjudication, provisional pending live reconciliation: permanent CoordinationNoStandby firing is correct advisory behavior for an explicitly coordinated one-replica deployment. The chart default is an uncoordinated singleton, so default installs emit no coordination series. Excluding single-replica deployments from the current metric surface would also hide an intended multi-replica deployment that has lost its only standby because no desired-replica label distinguishes them. Offline checks passed (rules-check, helm-lint, test-python), but live evaluation and Grafana server acceptance remain separate evidence.

Baseline live standby scrape on the immutable current-image sibling: 7 metric families were served: tailscale2otel_build_info_ratio, tailscale2otel_coordination_leader_ratio, tailscale2otel_export_duration_seconds_bucket/count/sum, tailscale2otel_metrics_scrape_in_flight, and target_info. No collector or per-tailnet tailscale_* family was present. The contemporaneous leader served 110 metric families including device and per-tailnet series. The standby /metrics endpoint returned successfully while its readiness probe correctly remained 503.

Baseline live demotion measurement: after the Lease holder was changed to the prior standby, the old leader returned leader=1 plus collector series for 14 completed scrapes. Scrape 15, 10.637 seconds after the Lease change, was the first demoted scrape and carried leader=0 with the collector family absent. Thus the observed demotion lag was 10.637 s at this harness cadence, and zero post-demotion scrapes leaked the former full surface.

Baseline live Lease-deletion/conflicting-replacement measurement: after deleting the live Lease and creating the replacement with the prior standby as holder, the old leader still served collector series at 0.336 s. The replacement first served collector series at 5.409 s while the old leader was still active. The old leader stopped serving at 10.799 s (its port-forward disconnected as the process exited). Directly observed duplicate-active overlap: 5.390 s, measured from first scrape with both collector surfaces to the first scrape where the old surface was gone. The replacement remained Lease holder with the expected transition count.

Baseline API-server-unavailability measurement: a temporary egress-deny NetworkPolicy selected only the live leader. Lease renewTime did not advance after the policy applied. The leader still served collector series at 0.933 s and its process/port-forward disconnected by 12.875 s; the unblocked standby then acquired the Lease and leaseTransitions advanced. The policy was deleted, its exact name read back absent, and the pod label was removed. This proves fail-closed demotion under an unreachable API server; it does not claim ServiceAccount RBAC proof through the tailnet proxy.

Baseline configured clock-skew observation: the live Lease was changed to a synthetic holder with acquire/renew timestamps 5.000 s ahead of the observer clocks. The incumbent failed renewal and completed shutdown 11.056 s after injection (process log). A replica became the real holder 19.641 s after injection and subsequently served coordination leader=1 plus the collector surface, yielding an 8.585 s observed no-leader interval between incumbent shutdown and new holder. The future timestamp did not add five seconds to takeover because client-go ages an unchanged record from local observation time. The harness's 200 ms active-scrape deadline was too short for a full collector gather, so no scrape-based stop timestamp is claimed for this leg; Lease and process timestamps are the evidence. The synthetic record was replaced by the normally renewing real holder without further mutation.

Post-fix live Lease-deletion/conflicting-replacement repeat on the exact-head prerelease image, using the same concurrent 200 ms scrape cadence as baseline: the former leader's metrics listener had already closed by the first completed scrape at 204 ms, so its collector surface stopped within 204 ms of replacement creation. The replacement first served collector telemetry at 6.673 s. No scrape observed both collector surfaces active; directly observed duplicate-active overlap was 0 ms, down from the 5.390 s baseline. The resulting non-overlap interval between the first old-surface-absent observation and the replacement's first active scrape was 6.469 s. The replacement remained Lease holder and the recorded transition count matched the value created by the harness.

Coordination-rule firing history for the later promotion task: the isolated sibling exported OTLP process telemetry only, with every collector disabled, and live queries returned one leader, one standby, and a completed-handover sum of 1. The initial evaluator snapshot landed before the first export and showed Normal (NoData). After one complete 60 s evaluation interval, CoordinationNoLeader, CoordinationSplitBrain, CoordinationNoStandby, and the new CoordinationFlapping rule each reported the sibling Normal; all four evaluators were healthy, carried no last error, and had no pending or firing state. This is non-firing baseline evidence, not evidence for paging promotion.

Confirmed live revert: the temporary sibling Deployment, both ReplicaSets, all eight pod names observed across the run, ConfigMap, Secret, ServiceAccount, Role, RoleBinding, Lease, both temporary NetworkPolicy names, and the possible Service/PVC names were each read back absent by exact name; a broad inventory found no remaining Wave 11 object. The managed workload matched its pre-run snapshot exactly for Deployment UID, generation, resource version, replica count, image reference, and PVC claim, and its configuration remained uncoordinated dual delivery with file-backed checkpoints. All lab access used the tailnet Kubernetes context; AWS SSO and the direct EKS profile were not touched.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Completed live measurement and revert for implementation 84776837 plus correction fe3c3cc6. Baseline: standby served seven process families and no collector or per-tailnet family; demotion changed on scrape 15 at 10.637 s with zero post-demotion leaks; Lease replacement produced 5.390 s duplicate-active overlap; API loss stopped the leader by 12.875 s; 5.000 s configured skew produced an 8.585 s no-leader interval. Post-fix, the old surface was absent by 204 ms and duplicate-active overlap was 0 ms; replacement became active at 6.673 s. All four coordination rules evaluated the isolated sibling Normal with healthy evaluators after one full interval. Permanent NoStandby firing remains correct advisory behavior for an explicitly coordinated singleton because the exported metric cannot distinguish desired replicas from a lost standby. Every temporary object was read absent by exact name and the managed Deployment, config, and PVC matched the pre-run snapshot. CI 33868900332 attempt 1 failed on runner-only deprecated ListWatch fields; after the evidence-led correction, exact-head CI 33870727006 attempt 1 succeeded. just check, just gen, and just --fmt --check pass.
<!-- SECTION:FINAL_SUMMARY:END -->
