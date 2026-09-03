---
id: TSO-0115
title: 'Prove coordinated Kubernetes mode on the lab, then revert'
status: Done
assignee:
  - '@codex'
created_date: '2026-09-02 15:48'
updated_date: '2026-09-03 19:00'
labels: []
dependencies:
  - TSO-0110
priority: medium
type: chore
ordinal: 116000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Waves 5 and 6 built Lease-based coordination, a Kubernetes checkpoint backend, sharding, compression and legacy migration, and none of it has ever run outside tests. The lab is configured for checkpoint.store=file, so Wave 6's rollout proved only that the new image starts and stays healthy with the Kubernetes path inactive. Its own report lists legacy-to-shard migration, the migration marker, shard annotations, compressed sizes, legacy reopen and Kubernetes leader state as unproven live.

Owner authorisation 2026-09-02: run a scoped temporary cycle on the lab that enables coordinated mode with its RBAC, gathers the evidence, and reverts to the current configuration. Wave 6 already demonstrated that rollout and rollback of the image are clean.

Live verification is the root agent's work and is never dispatched to a lane. Keep lab names, addresses, identifiers and credentials out of tracked files - record shapes, counts and sizes, not instances.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A coordinated deployment elects a leader through the Lease, and leadership is observed rather than inferred from the absence of errors
- [x] #2 A pre-existing single-ConfigMap checkpoint is migrated to shards live, and the shard count, compressed sizes and compression ratios are recorded
- [x] #3 The retained legacy object is confirmed present and reopenable by the previous release, proving the rollback path rather than assuming it
- [x] #4 The namespace-wide ConfigMap grant is confirmed sufficient and no broader permission was needed at runtime
- [x] #5 The lab is returned to its prior configuration and image, verified ready, with every temporary object removed and named in the record
- [x] #6 Anything that could not be proven is recorded as unproven with its reason, never reported as passed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 8 root live cycle:
1. Record the lab context, namespace, workload shape, exact image digest, configuration, readiness, and existing coordination/checkpoint/RBAC objects without exposing instance identifiers in tracked text.
2. Wait for the final Wave 8 immutable image from exact-head CI/automatic RC publication; do not substitute an older image as final-head proof.
3. Preserve the exact prior values, create a scoped legacy checkpoint, and temporarily enable coordinated Kubernetes mode with chart RBAC.
4. Observe Lease leadership plus application status/metric evidence; record shard count, compressed and decoded sizes, ratios, migration marker, and reconciliation baseline.
5. Run the previous release against the retained legacy object to prove rollback reopenability, then return to the Wave 8 image to exercise rollback-era reconciliation.
6. Restore the exact prior image and configuration, verify readiness, and delete every temporary Lease, legacy/shard ConfigMap, Role, and RoleBinding by name.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Live-cycle outcome: Parked before the first mutation. A successful read-only baseline captured the existing workload at 1/1 ready on immutable digest sha256:a440b30b3256de4a57cf42703040605172ca7a9984e30e07ef6bf8b4b1d69724 with checkpoint.store=file, one bound 20 Gi state volume, and no Lease, checkpoint shards, coordination Role, or RoleBinding. The final Wave 8 image for code head 1c088cea1dbdd9fbcd0d59086953bada2a9ff69f was published as v4.1.0-rc.66 at multi-architecture digest sha256:14d2718b23001074c78b3082d44779fae387366da0f1f9131ed9660e793bbced. Before mutation, the authorized EKS context became unavailable because its cached AWS SSO session had expired. This run was not authorized to establish a login and did not retry the failed context. Therefore Lease leadership, live migration and shard sizes, previous-release reopen, changed-resourceVersion reconciliation, and runtime RBAC sufficiency are unproven. No temporary ServiceAccount, Role, RoleBinding, Deployment, Lease, legacy ConfigMap, or shard ConfigMap was created, so no lab change or cleanup was required. Narrow reversible resume design: after an operator refreshes the SSO session, re-confirm the baseline and create a separate two-replica coordinated deployment in the same namespace with dedicated chart-equivalent RBAC, leaving the Argo-managed workload untouched because self-heal and prune are enabled. Seed one legacy checkpoint, observe Lease plus status plus metric leadership, record each shard size and migration baseline, switch the temporary deployment to the prior digest to advance retained legacy state, switch back to sha256:14d2718b23001074c78b3082d44779fae387366da0f1f9131ed9660e793bbced to prove reconciliation, exercise allowed and denied RBAC, delete every temporary object by exact name, and finally re-confirm the managed workload at its original digest and file-backed configuration. Question for Rob: confirm that this isolated sibling deployment is the desired resume shape rather than temporarily changing the Argo application.

Resume 2026-09-03: Rob restored the AWS SSO session. Re-entering at the documented read-only baseline check; no prior temporary object exists.

Resume outcome (final-head 86cebba, RC.67 image sha256:35c721d42406a6d2e9d33ea70565c2d1f0a29e36c0ea65c42a22f8d0ffcf2b0c): the isolated sibling deployment bootstrapped at one replica, migrated the seeded legacy object, then scaled to two replicas. The Lease holder and advancing renewTime, both pods’ /api/status.json states (one leader and one standby naming the same holder), and tailscale2otel_coordination_leader_ratio=1 for the leader were observed directly. The migration retained and marked the legacy object and created exactly two shards: tailscale2otel-wav-checkpoints-563d104f4357ba5d104736f0da704397 (flowlogs: 796,901 compressed bytes, 2,627,491 decoded bytes, 3.297136 decoded/compressed ratio) and tailscale2otel-wav-checkpoints-c86203ac532312ebf37d1918079ff8a8 (auditlogs: 119 compressed bytes, 94 decoded bytes, 0.789916 ratio). Both carried their shard-key annotation and the marked legacy resourceVersion baseline. The effective ServiceAccount boundary was verified against the direct EKS endpoint because the tailnet API proxy authenticates the operator even when kubectl --as/--token is supplied: Lease get/update by exact name plus create and ConfigMap get/list/update/create were allowed; Lease list/watch/patch/delete, ConfigMap watch/patch/delete, secrets and pods were denied. No runtime authorization error occurred.

Rollback proof and discovered defect: RC.56 image sha256:adf4220626ed9d00e277325db0119697d1bef74b037166ae0c27316c41ac9b2c acquired the Lease, reopened the retained single-ConfigMap object, advanced both real polling cursors, grew it to 1,299 bytes and changed its resourceVersion. RC.56 removed the migration marker while updating. On re-upgrade, RC.67 restored the marker and advanced both shard baseline annotations to the new legacy resourceVersion, but did not apply the rollback image’s newer values: audit remained about 29 seconds older and flowlogs about 81 seconds older than legacy. This newest-wins reconciliation is therefore failed live evidence, not a pass; it is outside TSO-0115 AC 1-5 and must be triaged before claiming round-trip rollback safety. The run contract allowed edits only to the five commissioned tasks, so no sixth Backlog task was created. Narrow decisions taken: bootstrap one replica before scaling to two; use read-only poll sources only for cursor advancement; disable temporary Pyroscope, pprof, Grafana annotations and GeoIP download; restore stream mode for the re-upgrade comparison.

Cleanup: deployment/tailscale2otel-wave8, serviceaccount/tailscale2otel-wave8, role/tailscale2otel-wave8-coordination, rolebinding/tailscale2otel-wave8-coordination, lease/tailscale2otel-wave8, configmap/tailscale2otel-wave8-checkpoints, configmap/tailscale2otel-wav-checkpoints-563d104f4357ba5d104736f0da704397 and configmap/tailscale2otel-wav-checkpoints-c86203ac532312ebf37d1918079ff8a8 were deleted and each exact name read back absent. The managed workload was re-confirmed 1/1 ready on its untouched original digest sha256:a440b30b3256de4a57cf42703040605172ca7a9984e30e07ef6bf8b4b1d69724 with imagePullPolicy Always, checkpoint.store=file, checkpoint.evidence_store=file and no coordination block.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked before mutation at the expired AWS SSO boundary. The Wave 8 image is available at sha256:14d2718b23001074c78b3082d44779fae387366da0f1f9131ed9660e793bbced, the prior baseline was recorded, no temporary object was created, and the precise isolated-deployment resume cycle is documented. Only the explicit unproven-accounting criterion is complete; the live behavioral criteria remain unchecked.

Resumed after AWS SSO renewal and completed AC 1-5 against final tracker head 86cebba / RC.67. Live evidence proves active-passive Lease leadership, two-shard gzip migration with measured sizes, RC.56 legacy reopen and cursor writes, chart-minimal runtime RBAC, and exact baseline restoration with every temporary object removed. The additional RC.56-to-RC.67 round trip exposed a real cursor-reconciliation defect: metadata baselines advanced but newer rollback cursors did not; that observation is explicitly failed and queued for Rob’s triage rather than reported as proven.
<!-- SECTION:FINAL_SUMMARY:END -->
