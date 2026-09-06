---
title: High availability
description: Run coordinated Kubernetes replicas with one active exporter, shared cursor checkpoints and separate local state.
---

# High availability

The default `coordination.mode: none` permits one active process for each tailnet. Kubernetes mode
uses a Lease to elect one active exporter from two or three replicas. The leader runs collectors,
receivers and WAL replay for the whole configured fleet. Standbys do no collection or receiving.
Compose and standalone binary deployments remain singleton deployments.

## Helm configuration

Start with the [Helm installation](installation.md#helm) and its credential setup, then
apply these additional values:

```yaml
replicaCount: 2
config:
  coordination:
    mode: kubernetes
    lease_name: tailscale2otel
  checkpoint:
    store: kubernetes
persistence:
  enabled: true
```

Use a distinct Lease name for each independent exporter installation in the namespace. All replicas
of one installation must use the same Lease and namespace. The chart defaults the coordination
namespace to the release namespace; a raw application config defaults it to `default`.

Coordinated mode renders a StatefulSet with `Parallel` pod creation and the default Kubernetes
`RollingUpdate` update strategy. Singleton mode renders a Deployment with `Recreate`. The chart
rejects more than three coordinated replicas and rejects multiple replicas in `none` mode.

Keep the generated ServiceAccount and Roles, or supply equivalent permissions. Lease operations
use the coordination namespace. ConfigMap checkpoint shards require namespace-wide create, get,
list and update permissions because their names are derived dynamically. A separate Role grants
pod get/patch in the release namespace so the coordinator can set its own leader label. Kubernetes
RBAC cannot restrict that grant to the caller's pod; use a namespace appropriate for this access.

## Readiness and traffic routing

All replicas keep the admin listener and, when enabled, the Prometheus listener running.
A standby becomes Ready once coordination starts. The leader also checks collector startup and
component health before reporting Ready.

The chart's admin, Prometheus, streaming and webhook Services select
`tailscale2otel.m7kni.io/role: leader`. Standby readiness therefore does not send receiver traffic
to a standby. The coordinator clears its own label before campaigning and on demotion, and sets
it after acquiring the Lease. A handover can briefly leave a Service without an endpoint.

A direct Prometheus scrape of a standby returns process metrics only. A leader returns the full
metric set; the choice is made on each scrape, including after demotion. Use per-pod monitoring to
observe standbys, since the Prometheus Service selects the leader. Ready pod count alone does not
prove there is one active leader or that receiver traffic reaches it.

If renewal fails for `renew_deadline`, or the Lease is deleted or replaced while held, the leader
stops active work. Check the coordination status and handover metrics alongside Service endpoints.
The defaults are a 15-second Lease, a 10-second renewal deadline and a 2-second retry period.
The required order is `lease_duration > renew_deadline > 1.2 * retry_period > 0`.

## Shared cursors and local state

`checkpoint.store: kubernetes` requires Kubernetes coordination. It stores compressed checkpoint
shards in ConfigMaps derived from the Lease name and collector namespace. Each shard must fit the
ConfigMap size limit; startup checks capacity for bounded dedup and object-store state. This shares
cursor progress across leaders without sharing a single-writer file.

Other state stays local to each replica:

| State | Handover behavior |
|---|---|
| `checkpoint.evidence_store` | Only `file` and `memory` are supported. ACL provenance on one pod is not replicated to another. |
| Ingress WAL | Accepted bodies remain on the accepting pod's volume. Another leader does not replay that pod's WAL. Preserve the volume and account for delayed replay when its owner becomes active again. |
| Persistent flow store | Each pod retains its own SQLite history. The new leader's view is not a merged history of the deployment. |
| In-memory dedup, enrichment and event history | Rebuilt or repopulated in the active process. Handover can leave gaps in local views and allow replayed records through. |

With `persistence.enabled: true`, each StatefulSet pod receives its own PVC. `persistence.existingClaim`
is rejected in coordinated mode. Size each claim for the enabled WAL and flow history as well as
checkpoint evidence. The chart does not replicate these files or provide exactly-once delivery.
Select one ingestion source per log type.

## Upgrade and rollback

Follow the [upgrade checklist](upgrading.md#upgrade-and-rollback-checklist), preserving each pod's
PVC and the checkpoint ConfigMaps. When moving from singleton Deployment to coordinated StatefulSet,
stop the old uncoordinated exporter before starting the new replicas; the two workload kinds can
otherwise overlap. An existing singleton claim is not automatically imported into per-pod claims.

For an existing coordinated release, apply the chart/image update and wait for StatefulSet rollout.
Verify one Lease holder, one leader-labelled pod, matching enabled Service endpoints, collector
progress on that leader, and fresh data at the destination. A standby returning 200 from `/readyz`
is expected and is not evidence of active collection.

For rollback, retain the state and use a chart/application pair whose routing behavior agrees.
Leader-selecting Services require an application that writes the leader label. Do not pair them
with an older build that never sets it. If reverting to a pre-sharding checkpoint writer, preserve
both legacy and sharded checkpoint objects for reconciliation on re-upgrade. The [upgrade
checklist](upgrading.md) also covers forward-only database migrations; restore a compatible backup
before starting an older binary when required.
