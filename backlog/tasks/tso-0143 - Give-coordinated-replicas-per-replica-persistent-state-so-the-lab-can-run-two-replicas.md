---
id: TSO-0143
title: >-
  Give coordinated replicas per-replica persistent state so the lab can run two
  replicas
status: Done
assignee:
  - codex-root
created_date: '2026-09-06 09:28'
updated_date: '2026-09-06 10:30'
labels: []
dependencies: []
references:
  - deploy/helm/tailscale2otel/templates/deployment.yaml
  - deploy/helm/tailscale2otel/templates/pvc.yaml
priority: medium
type: feature
ordinal: 144000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The chart renders a Deployment with one PersistentVolumeClaim (ReadWriteOnce by default) mounted at the state path. In coordinated mode (config.coordination.mode: kubernetes, replicaCount 2 or 3) that layout cannot run: a second replica on another node cannot attach the claim, and every writer on that volume is single-writer by design (SQLite flow store, ingress WAL, the file checkpoint and evidence stores, the GeoIP download directory, and an operator sidecar's Tailscale node state mounted via extraVolumeMounts subPath). The HA design (TSO-0033) already rules out a shared RWX volume as a coordination substrate. Wave 8 and Wave 11 proved coordinated mode only on a temporary sibling with no volume at all, so no persistent coordinated deployment has ever existed, which is why the four coordination alert rules have no history to judge (TSO-0133). Owner decision 2026-09-06: the chart grows per-replica storage first, then the lab flips to two coordinated replicas. The natural shape is a StatefulSet with volumeClaimTemplates rendered when coordination.mode is kubernetes, keeping the Deployment for the singleton so existing installs do not migrate; a renamed workload kind is a breaking change for anyone with persistence.existingClaim, so state how an existing claim is carried or why it cannot be. Checkpoints already have a shared Kubernetes backend; the evidence store has only file or memory, so state which state is per-replica and what a hand-off loses. An operator sidecar in extraContainers must be able to give each replica its own node identity; the chart owns the volume, not the sidecar.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 With config.coordination.mode kubernetes and replicaCount above 1, the chart renders a workload whose replicas each get their own persistent volume, and helm template plus kubeconform with real schemas pass for replicaCount 1, 2 and 3 in both modes
- [x] #2 The singleton path is byte-identical to today for replicaCount 1 with mode none (helm template diff against the previous chart is empty), and a coordinated install with persistence.existingClaim set fails at render with a message saying per-replica claims replace it
- [x] #3 The chart README states which state is per-replica (flow store, WAL, GeoIP, evidence, sidecar state) and which is shared (checkpoint ConfigMaps, the Lease), and what a leadership hand-off loses; the generated README and values.schema.json are regenerated with the pinned tools
- [x] #4 A rendered two-replica coordinated manifest passes configcheck and the chart's render-level security contracts, and a Kind or k3d cluster run proves two pods schedule on different nodes with distinct claims, one leader and one standby
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 15 contract: Lane B uses JUDGMENT+EXECUTION, gpt-5.6-terra/high, self-contained, no delegation; public chart contract requires judgement while root owns integration. Extract the shared pod template, preserve singleton output, add coordinated StatefulSet per-replica storage and soft anti-affinity, reject existingClaim, document handoff semantics, bump chart to 0.34.0. Validate render contracts, pinned generation and local Kind proof; root repeats proof from committed source and owns gate/review/commits/workflow evidence.

Root integration completed the shared-template consumer update, corrected explicit-affinity scope, and added Parallel/OnDelete render guards after observing standby readiness behavior. The automatic upgrade policy is a batched owner question for the next wave.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Preflight reconciliation: preserve the existing rejection of mode none with replicas 2 or 3, despite AC1 wording saying both modes pass. One volumeClaimTemplates entry creates one distinct claim per replica (two claims for two replicas); do not create two state volumes. Skip live verify-deploy because the launch instruction forbids all live-system contact. Only local Kind named tso-0143 is permitted. Singleton extraction must retain byte-identical rendered output except chart-version label.

Phase-0 just check passed at baseline 9414328c573a21ce0aa73a69eecc0c54d980dbee (exit 0). Independent implementation lanes now start together; root owns full integration and final checks.

Integration review identified the shutdown-budget guard test reading deployment.yaml literally; root will update that consumer if the guard moves to the shared pod template. A governing headless Service should provide DNS identity only, with no listener ports, if real schema and Kind validation accept it; existing credential-gated per-listener Services remain operator opt-ins.

Root integration decision from the Kind readiness evidence: Kubernetes OrderedReady would prevent a third replica from being created after an unready standby, and RollingUpdate would wait forever on that standby. Use podManagementPolicy Parallel and updateStrategy OnDelete for the new coordinated StatefulSet, retaining readiness-based traffic exclusion. This is the narrow chart-only solution; document explicit operator-controlled pod replacement, no Helm --wait/--atomic, and batch the automatic-upgrade policy question into the report. No runtime readiness, Lease, checkpoint or singleton semantics change.

Root final diff review found an affinity context regression: replacing with by if left toYaml pointing at the entire chart context. The earlier override test checked absence of podAntiAffinity but did not verify the provided nodeAffinity survived. Stopped the first integrated gate while still in formatting; add a failing exact-affinity-shape contract, then serialize only Values.affinity. This also prevents unrelated values from entering workload spec fields.

Root Kind proof at committed chart 5d966fe82759fb1a6c8fc9222b1b7d4e4e261133 passed independently: two Running pods on distinct workers, two distinct Bound RWO claims, authenticated status showed one leader and one standby, and Lease holder matched the leader. Scaling the same local release to three created a third Running pod and distinct Bound claim while the standby remained unready, validating Parallel creation. The local cluster was deleted and kind get clusters returned none. Replica standby readiness remains intentionally false; use OnDelete/manual replacement for updates. Both lane and root each created/deleted exactly one local cluster.

Delivered 5d966fe82759fb1a6c8fc9222b1b7d4e4e261133: chart 0.34.0 adds a coordinated StatefulSet with one state claim per replica and retains the singleton Deployment. Default and custom-affinity/persistent singleton renders match the baseline byte-for-byte after normalizing only the chart version label. Coordinated existingClaim fails with persistence both enabled and disabled. Root schema checks passed for singleton 1 and coordinated 1/2/3 with zero skipped resources; none-mode 2/3 correctly remain rejected per the frozen contract. Final Helm suite: 502 assertions passed; configcheck passed. Lane and root independently proved two Running pods on different local workers, distinct Bound claims, leader/standby status and matching Lease holder. Root additionally proved third-replica creation. Both local clusters were deleted and final cluster inventory was empty. Parallel creation and OnDelete updates preserve standby readiness exclusion; manual standby-first pod replacement and Helm wait limitations are documented. Full local just check passed at 5d966fe82759fb1a6c8fc9222b1b7d4e4e261133; just --fmt --check passed. Two full regenerations were byte-stable on the second; docs/metrics.md, the coverage manifest and the chart schema are unchanged. CodeRabbit completed with zero findings. Exact-code-head CI 34026607134, Helm 34026607302, Release 34026607341 and auto-rc 34026995398 all succeeded on attempt 1. RC v5.0.0-rc.31 resolves to that code commit. Known local skips: TestRuleCountsFromRealACL (fixture absent), TestDumpFlowsJSON (output not requested), TestDefaultCheckpointPath_XDGStateHomeWins (macOS). Live verify-deploy was not run because live access is excluded. The current grafana-sync workflow is path-filtered; this wave changes no matching path and triggered no Grafana write workflow. PR #585 remains owner-held.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Delivered 5d966fe82759fb1a6c8fc9222b1b7d4e4e261133: chart 0.34.0 adds a coordinated StatefulSet with one state claim per replica and retains the singleton Deployment. Default and custom-affinity/persistent singleton renders match the baseline byte-for-byte after normalizing only the chart version label. Coordinated existingClaim fails with persistence both enabled and disabled. Root schema checks passed for singleton 1 and coordinated 1/2/3 with zero skipped resources; none-mode 2/3 correctly remain rejected per the frozen contract. Final Helm suite: 502 assertions passed; configcheck passed. Lane and root independently proved two Running pods on different local workers, distinct Bound claims, leader/standby status and matching Lease holder. Root additionally proved third-replica creation. Both local clusters were deleted and final cluster inventory was empty. Parallel creation and OnDelete updates preserve standby readiness exclusion; manual standby-first pod replacement and Helm wait limitations are documented. Full local just check passed at 5d966fe82759fb1a6c8fc9222b1b7d4e4e261133; just --fmt --check passed. Two full regenerations were byte-stable on the second; docs/metrics.md, the coverage manifest and the chart schema are unchanged. CodeRabbit completed with zero findings. Exact-code-head CI 34026607134, Helm 34026607302, Release 34026607341 and auto-rc 34026995398 all succeeded on attempt 1. RC v5.0.0-rc.31 resolves to that code commit. Known local skips: TestRuleCountsFromRealACL (fixture absent), TestDumpFlowsJSON (output not requested), TestDefaultCheckpointPath_XDGStateHomeWins (macOS). Live verify-deploy was not run because live access is excluded. The current grafana-sync workflow is path-filtered; this wave changes no matching path and triggered no Grafana write workflow. PR #585 remains owner-held.
<!-- SECTION:FINAL_SUMMARY:END -->
