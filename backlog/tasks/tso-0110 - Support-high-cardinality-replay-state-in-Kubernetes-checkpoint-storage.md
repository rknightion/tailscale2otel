---
id: TSO-0110
title: Shard and compress Kubernetes checkpoint storage to remove the 1 MiB ceiling
status: To Do
assignee: []
created_date: '2026-09-01 21:38'
updated_date: '2026-09-02 05:17'
labels: []
dependencies:
  - TSO-0111
priority: high
type: bug
ordinal: 111000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Kubernetes checkpoint backend stores the entire shared checkpoint map as one JSON value in one ConfigMap data entry, deliberately, so that a single resourceVersion-guarded update covers every cursor atomically. That ceiling is 1,048,576 bytes and it is reachable at stock defaults: one object-store feed at the default max_seen_keys of 5000 already serialises to 785,001 bytes, and three feeds reach 2,355,001. TSO-0111 converts the resulting runtime failure into a startup rejection; this task removes the ceiling.

Owner decision 2026-09-02: shard the map into one ConfigMap per collector namespace and gzip each shard into binaryData.

Sharding along the collector boundary is chosen because cross-collector atomicity buys nothing - collectors advance their cursors independently, and no invariant spans two of them - while per-collector atomicity, which is the property that actually matters, is preserved by a per-shard resourceVersion. The boundary already exists in the key namespace the scheduler assigns, so the shard key needs no new concept. Compression is applied because the payload is close to a worst case for gzip: base64 keys over a shared prefix, mapped to timestamps that differ in their last few characters.

Rejected alternatives, with their reasons, so they are not re-proposed: gzip alone keeps global atomicity but only raises the ceiling and returns at higher cardinality; moving the seen-set to SQLite or a PVC makes durable local storage effectively mandatory for object-store ingestion under coordination, which is the deployment shape coordination exists to serve; clamping max_seen_keys silently degrades restart dedup and caps throughput on exactly the deployments that scale out.

Oversize handling must keep the Wave 5 property that a payload which still cannot fit fails visibly and never truncates, and the deposed-leader guarantee that a stale writer cannot overwrite current state must hold per shard.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Checkpoint state is sharded across one ConfigMap per collector key namespace, and each shard's payload is gzip-compressed into binaryData
- [ ] #2 Every shard write is guarded by that shard's own resourceVersion, so a deposed leader cannot overwrite a current shard, and a conflict is surfaced rather than retried into an overwrite
- [ ] #3 The three measured configurations in TSO-0111 persist successfully, and a shard that still exceeds the limit fails visibly without truncating or dropping keys
- [ ] #4 A store opened over checkpoint state written by the single-ConfigMap backend continues to work, and the migration path is exercised by a test rather than assumed
- [ ] #5 Shard count, per-shard compressed size and the compression ratio are observable, so an operator can see headroom before it is exhausted
- [ ] #6 The Helm chart's checkpoint RBAC covers every ConfigMap the sharded store creates, and no rule is broader than the objects it needs
- [ ] #7 Tests cover ordinary poll cursors, the high-cardinality seen-set and gap state, and the deposed-leader conflict path per shard
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
