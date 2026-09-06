---
id: TSO-0144
title: >-
  Route receiver and admin traffic by a leader pod label so standbys can be
  Ready
status: To Do
assignee:
  - '@codex-root'
created_date: '2026-09-06 11:21'
labels: []
dependencies: []
references:
  - internal/coordination/coordination.go
  - internal/app/readiness.go
  - internal/app/coordination.go
  - deploy/helm/tailscale2otel/templates/workload.yaml
  - deploy/helm/tailscale2otel/templates/service.yaml
  - deploy/helm/tailscale2otel/templates/role.yaml
priority: high
type: feature
ordinal: 145000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Coordinated mode uses readiness as its traffic selector: a standby answers 503 on /readyz so the per-listener Services route only to the leader (TSO-0033 option A). TSO-0143 showed what that costs once the workload is a StatefulSet with per-replica claims: automatic rolling updates wait forever for a standby to become Ready, so the chart shipped updateStrategy OnDelete, which means an image tag change applied by a GitOps controller never replaces existing pods; helm --wait and --atomic can never succeed; and Argo CD built-in StatefulSet health reports Progressing for as long as readyReplicas is below replicas, which with a permanent unready standby is forever. Owner decision 2026-09-06: adopt the established active-passive pattern (Vault, Patroni, CloudNativePG): every replica that has finished startup is Ready, the leader marks its own pod with a role label the moment it holds the Lease, and the Services that must reach only the leader select that label. The label key is frozen as `tailscale2otel.m7kni.io/role` with the single value `leader`; absence means not leader. No new config key: the pod namespace comes from the mounted service account and the pod name is the coordination identity. This supersedes the not-ready standby line in the TSO-0033 design record.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 In coordinated mode a standby that has completed startup answers 200 on /readyz; the leader keeps today collector and component gating; singleton behaviour is unchanged
- [ ] #2 The pod holding the Lease carries the label `tailscale2otel.m7kni.io/role=leader` within one retry period of acquiring it; a standby, a stepped-down pod and a pod that has just started never carry it (a restarting pod clears a stale label before campaigning); a pod that lacks permission to patch its own labels fails startup in coordinated mode with an error naming the missing pods patch grant
- [ ] #3 In coordinated mode every per-listener Service selects the role label in addition to the selector labels, while the headless Service, the workload selector, PodMonitor, ServiceMonitor and NetworkPolicy do not; a Role and RoleBinding in the release namespace grant get and patch on pods; the StatefulSet uses RollingUpdate and no longer sets OnDelete; the singleton render stays byte-identical except for the chart version; the chart is 0.35.0; render tests cover each of these and the README documents the routing and rollout contract
- [ ] #4 A Kind proof with two replicas shows both pods Ready, the streaming Service EndpointSlice holding only the leader pod, the label and the endpoint moving to the surviving pod within one lease duration after the leader pod is deleted, and a helm upgrade --wait that completes
- [ ] #5 docs/configuration.md coordination text no longer says standbys remain unready, and the TSO-0033 record carries a note pointing at this task; just gen leaves no diff
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
