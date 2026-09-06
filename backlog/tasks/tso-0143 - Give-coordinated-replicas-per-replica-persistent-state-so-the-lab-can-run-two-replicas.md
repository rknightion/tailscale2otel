---
id: TSO-0143
title: >-
  Give coordinated replicas per-replica persistent state so the lab can run two
  replicas
status: To Do
assignee: []
created_date: '2026-09-06 09:28'
updated_date: '2026-09-06 09:28'
labels: []
dependencies:
  - TSO-0142
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
- [ ] #1 With config.coordination.mode kubernetes and replicaCount above 1, the chart renders a workload whose replicas each get their own persistent volume, and helm template plus kubeconform with real schemas pass for replicaCount 1, 2 and 3 in both modes
- [ ] #2 The singleton path is byte-identical to today for replicaCount 1 with mode none (helm template diff against the previous chart is empty), and a coordinated install with persistence.existingClaim set fails at render with a message saying per-replica claims replace it
- [ ] #3 The chart README states which state is per-replica (flow store, WAL, GeoIP, evidence, sidecar state) and which is shared (checkpoint ConfigMaps, the Lease), and what a leadership hand-off loses; the generated README and values.schema.json are regenerated with the pinned tools
- [ ] #4 A rendered two-replica coordinated manifest passes configcheck and the chart's render-level security contracts, and a Kind or k3d cluster run proves two pods schedule on different nodes with distinct claims, one leader and one standby
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
