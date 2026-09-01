---
id: TSO-0033
title: Design and ship an HA / multi-replica deployment story
status: Done
assignee: []
created_date: '2026-08-30 09:02'
updated_date: '2026-09-01 20:01'
labels: []
milestone: m-8
dependencies: []
priority: medium
ordinal: 36000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The exporter is a deliberate singleton today (single-replica Helm chart, file checkpoints, in-memory dedup, single-writer SQLite flow store, ingress WAL), but users are running it on Kubernetes and multi-replica deployments will become a real demand. Explore the full range: simple lease-based active-passive failover (k8s operator / leader-election pattern) through per-tailnet work sharding up to Alloy-style consistent-hash clustering of poll work, and split-role designs (horizontally scaled receiver tier + leased poller tier). External dependencies (k8s Lease API, redis, memberlist) are in scope where justified. Grafana Alloy is prior art worth mining for how it shards scrape targets across cluster peers without duplicate scrapes. Key hazards to design around: duplicate series emission (service.instance.id semantics, counter resets), checkpoint/WAL/dedup/SQLite state ownership, and failover behaviour. First deliverable is an options proposal recorded on this task for a decision; implementation follows as separate work once a direction is chosen.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An options proposal (3-5 architectures, active-passive lease through sharded multi-pod) with state-handling, duplicate-emission analysis, dependencies, migration path and effort per option is recorded on this task
- [x] #2 A comparison table and flat recommendation with suggested phasing is included
- [x] #3 A direction is chosen and recorded before any implementation work is split out
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Options proposal (research + design pass, 2026-08-30)

TL;DR: today the process is a hard singleton — the Helm chart refuses `replicaCount != 1`, and every piece of durable/derived state (checkpoint JSON, ingress WAL, dedup sets, SQLite flow store, nodemetrics delta baselines, rollup buffers) is process-local and single-writer by design. The cheapest correct HA is **k8s Lease-based active-passive failover** (option A): all state stays per-replica, the lease decides who emits, failover in ~15s, no new runtime architecture. Recommendation: ship A first, built as "lease-scoped work units" where the v1 unit is the whole process — then, if MSP/multi-tailnet demand materializes, widen the same machinery to **one lease per tailnet** (option C) since `tailnetRuntime` is already a self-contained shard. Alloy-style gossip/consistent-hash clustering (option D) is the wrong tool here: the natural work unit (a tailnet) is too coarse and too few for a hash ring to pay for itself, and active-active with backend dedup (option E) is unavailable on the OTLP path. A separately scaled receiver tier (option B) is not worth the enrichment-cache split it forces.

### 1. What makes HA hard today (state inventory)

| State | Where | Sharing/writer model | HA consequence |
|---|---|---|---|
| Poll cursors (checkpoints) | `internal/collector/checkpoint.go:118-229` — one atomic JSON file (`checkpoint.file_path`), keys namespaced per tailnet (`Namespaced`, `checkpoint.go:248`) | Single writer assumed; `persistLocked` rewrites the **whole map** on every Set | Two processes sharing the file clobber each other's keys even for disjoint tailnets — a shared RWX volume is NOT a safe coordination substrate. A replica without the file cold-starts from `initial_lookback` → re-emits that window (at-least-once, duplicate logs) |
| Scheduler window semantics | `internal/collector/scheduler.go:418-455` | Checkpoint advances only on success; `ReplayOverlap` deliberately re-reads | Already at-least-once. Failover duplication is a *bounded extension* of an existing property, not a new violation |
| Ingress WAL | `internal/ingresswal/wal.go:145` (owner-locked store), flock at `platform_unix.go:128`, `.owner.lock` (`wal.go:218`), `ErrOwnership` (`wal.go:30`) | Exclusive per-process by design; replay drains before listeners open (`internal/app/ingresswal_app.go:245`) | Cannot be shared. Entries pending on a dead pod are stranded until that pod (or its PVC) returns. Replay is documented at-least-once (`config.example.yaml:506-509`) |
| Flow/audit dedup sets | `internal/dedup/dedup.go` (bounded FIFO SHA-256 sets), per-runtime `flowDedup`/`auditDedup` (`internal/app/tailnetruntime.go`), process-global webhook cross-dedup (`internal/app/app.go:118`) | In-memory, per-process | Any request that can land on a different replica (LB'd receivers, HEC retries) escapes the dedup window. Cross-source (poll vs stream) dedup only works when both paths share a process |
| SQLite flow store | `internal/flowstore/sqlitestore/store.go` — single background writer, per-tailnet digest-qualified file, explicit adoption (`adopt.go`) | Single-writer, local disk | Never shareable across pods (SQLite over network filesystems is a corruption hazard). Per-replica only; `/flows` history fragments across failovers |
| nodemetrics delta baselines | `internal/collector/nodemetrics/nodemetrics.go:15-17`, `prev` map (`:314`) | In-memory cumulative→delta baselines per target | Moving a target between replicas loses the baseline: first scrape by the new owner emits nothing (one-interval gap), no double-count. Benign but visible |
| Rollup buffers | `internal/app/rollupflusher.go` | In-memory aggregation flushed on interval | Lost on failover (bounded by flush interval); per-replica rollups are separate series, sums stay correct |
| Enrichment + ACL policy | `internal/enrich` DeviceCache, `aclpolicy.Store` — populated by the devices/acl/users collectors | In-memory, rebuilt each poll cycle | Cheap to rebuild, but a process that only runs receivers (no pollers) has no cache: flow/audit IP→name resolution degrades to `unknown`/`external`. This couples receivers to pollers |
| Identity | `service.instance.id` = hostname (or its hash) — `internal/app/options.go:249-262`; per-tailnet suffix `instanceFor` (`options.go:338`); distinct-per-tailnet requirement in `internal/telemetry/providerset.go:11-24` | Per-process | In k8s the hostname is the pod name, so replicas emit **distinct series** rather than colliding ones: two live emitters double-count `sum()` aggregations and duplicate every log record, but never corrupt a series |
| Admin/status surface | status page, `/flows`, API stats — all in-process | Per-process | Any sharded design fragments the operator view across pods |

Backend facts that bound the design space:

- **No HA dedup on the OTLP path.** Mimir's HA tracker (what makes Prometheus HA pairs work) is documented for Prometheus remote_write `cluster`/`__replica__` external labels only; no documented equivalent for OTLP ingest, and Loki has none for logs at all. Active-active identical emission is therefore not designable-around — duplicates land as duplicates.
- **Counter resets on failover are fine.** A new leader is a new process; with per-pod instance ids the old series goes stale and the new one starts — `rate()`/`sum by` across instances stays correct. The overlap window (two emitters during a lease handover) is the only duplication source, bounded by lease timing.
- **Tailscale API load:** sharding by tailnet does not increase per-tailnet request rates; active-active polling doubles them.

### 2. Prior art digest

**Grafana Alloy clustering** (studied from source): memberlist gossip over HTTP/2 (grafana/ckit), a 512-tokens-per-node consistent-hash ring, per-target ownership computed *locally and independently* on every peer (`cluster.Lookup(key, 1, OpReadWrite)` → "is peers[0] me?"), rebalance notifications debounced at 1/s. Transferable lessons:

1. Ownership is recomputed locally; there is **no handoff protocol** — Alloy accepts a brief double-scrape window during topology changes (documented as eventually consistent).
2. Every ambiguous branch fails toward availability: lookup error → claim locally; not-ready → do nothing; quorum timeout → proceed anyway.
3. **Local durable state is never migrated** (WAL, positions files). The new owner starts fresh; loss is bounded by retention, not prevented.
4. The losing side must *suppress its negative cleanup signal* for moved work (Alloy's staleness-marker suppression) — a direct analogue for any "device disappeared" style cleanup here.
5. Clustering is opt-in **per unit of work**, not just globally, to protect inherently-local workloads.
6. Readiness ("am I allowed to do work") is a separate layer above gossip membership ("am I in the cluster"), with min-size + fail-open timeout.

**k8s leader election**: `coordination.k8s.io/v1` Lease via client-go's `leaderelection` — battle-tested renew/steal semantics (typical 15s lease / 10s renew deadline / 2s retry), `OnStoppedLeading` conventionally terminates the process. Caveat: on apiserver unavailability the leader *steps down* at the renew deadline — an availability gap by design, traded against split-brain.

**otel-collector target allocator**: separate allocator deployment shards scrape targets across a collector StatefulSet via consistent hashing + HTTP SD. Proof that "external assigner + dumb workers" works, but adds a whole second deployment — heavier than this project needs.

**Prometheus Agent HA pairs**: work only because the backend dedups replicas via external labels — unavailable here, which kills the equivalent design.

### 3. Options

#### Option A — active-passive failover via k8s Lease (whole-process leader) — effort M

2 (or 3) replicas. Every pod starts, loads config, builds providers, serves `/healthz`; only the lease holder runs the runtime set (schedulers, receivers, WAL replay, rollup flusher, heartbeat). The standby campaigns on the Lease and reports **not ready**, so the Service routes receiver/admin traffic to the leader only. On losing leadership: exit(0), kubelet restarts the pod. On gaining: normal startup — WAL replay before listeners, checkpoint load, initial polls.

State stays per-replica. Two sub-options for checkpoints:
- *A1 (baseline)*: per-replica state (StatefulSet with per-replica PVCs, or Deployment with none). Failover cold-starts cursors → re-emits up to `initial_lookback` of logs (at-least-once, like a fresh install). WAL entries on the old pod's PVC replay when it returns as standby — delayed, not lost.
- *A2 (polish)*: new `checkpoint.store: kubernetes` backend writing the cursor map to a ConfigMap (or Lease annotations). `CheckpointStore` (`checkpoint.go:31-38`) is four methods and a small JSON payload; coalesce writes (~1/5s) to spare the apiserver. Failover resumes cursors near-seamlessly; the shared-volume question disappears; resourceVersion-guarded updates make a deposed leader's late write fail loudly.

Failover gap on crash ≈ leaseDuration + startup (15–30s vs minutes today). Duplication only in the deposed-but-not-dead window (bounded by renewDeadline). Config/Helm: `coordination.mode: none | kubernetes` (default none), lease name/namespace/timings; allow `replicaCount 1..3` when enabled (replace the hard fail at `deploy/helm/tailscale2otel/templates/deployment.yaml:13-15`), Role+RoleBinding on `leases`, optionally RollingUpdate (safe: new pod blocks on the lease — also removes *upgrade* downtime, the more common outage). Non-k8s: v1 documents "outside k8s, run one instance" (no regression); a redis/file lock backend can slot behind the same enum later if asked.

Failure modes: apiserver outage → leader steps down → emission gap until it returns (alert on it; no mitigation avoids split-brain); crash-looping leader flapping the lease (campaign only after preflight passes); two *releases* against one tailnet — out of scope, same as today. Add a `tailscale2otel.leader` info metric + status-page line.

#### Option B — split roles: scaled receiver tier + leased poller tier — effort M–L, NOT recommended

Receivers as a horizontally scaled Deployment; polling as an option-A singleton. Breaks: per-replica dedup windows escape on LB'd retries; poll-vs-stream cross-dedup impossible across processes (`source: both` would need to be refused); receiver pods have no enrich/ACL caches (fed by poll collectors) so flow/audit enrichment degrades to `unknown`/`external` unless a cache feed is built — a new distributed subsystem for cosmetic label quality; pod scale-down strands WAL entries unless PVCs are retained. Nothing suggests receiver throughput is an actual bottleneck; option A already covers receiver availability. Revisit only if a real ingest-throughput ceiling appears.

#### Option C — per-tailnet work sharding across replicas — effort L, follow-on

Natural shard is `tailnetRuntime` (`internal/app/tailnetruntime.go:29`) — already self-contained (own provider/client, emitter, caches, registry+scheduler, processors, flow store). Mechanism: **one Lease per tailnet**; every replica campaigns for every tailnet lease, runs runtimes for leases it holds, stops those it loses (needs per-runtime stop, not exit). Reuses A's machinery — A is "one lease over all work"; C widens the unit. No assigner, no gossip: lease contention distributes tailnets roughly evenly; a dead pod's tailnets are picked up within leaseDuration.

Checkpoints must stop being one shared file (per-tailnet ConfigMap owned by the lease holder, or per-replica files with bounded cold-start re-poll on movement). Receivers are the hard part: an inbound stream/webhook request for tailnet X must reach X's owner — per-tailnet Service gymnastics, an internal forward-to-owner hop, or receivers pinned to one replica (a mini option-B). None free; the main complexity tax. Identity caveat: `instanceFor` builds per-tailnet instance ids from pod hostname + tailnet (`options.go:338`), so a migrating tailnet churns every series — under sharding, the base should become a stable per-release value (safe: the per-tailnet suffix already guarantees distinctness, `providerset.go:11-24`). Only benefits MSP operators; caps at tailnet count. Risks: lease-thrash (mitigate with sticky campaigning + jittered acquisition), uneven load if one tailnet dwarfs the rest.

#### Option D — Alloy-style gossip + consistent-hash clustering — effort XL, rejected

Buys a non-k8s clustering story, smooth 1/N rebalance, and per-target sharding for nodemetrics on very large tailnets. Costs: gossip listener + mTLS surface on a hardened binary; readiness/min-size layer; rebalance plumbing into registry/scheduler; accept-brief-double-work at every topology change; no state migration (all of A/C's consequences too). With 1–5 tailnets the ring shards almost nothing, and most collectors are single whole-tailnet API calls that cannot shard finer. Steal its *lessons* (local ownership decisions, no handoff protocol, debounced rebalance, suppress-negative-signals-on-move, per-work-unit opt-in), not its architecture.

#### Option E — active-active with backend dedup — rejected on facts

No documented OTLP-path HA dedup in Mimir (HA tracker is Prometheus remote_write external-labels only) and none for Loki logs at all: every signal doubles, and per-tailnet API load doubles. Do not revisit unless the backend ships OTLP HA dedup.

### 4. Comparison

| | A: active-passive Lease | B: split receiver tier | C: per-tailnet leases | D: gossip/hash ring | E: active-active |
|---|---|---|---|---|---|
| Solves | crash + upgrade downtime | receiver throughput (not a real problem) | MSP horizontal scale + A | non-k8s clustering, giant-tailnet nodemetrics | nothing (backend can't dedup) |
| Duplicate risk | handover window only | dedup-window escapes on retries | move windows per tailnet | every topology change | permanent |
| State handling | all per-replica (opt. k8s checkpoint store) | per-pod WAL/dedup + enrichment split | per-tailnet scoping needed | same as C + no migration | n/a |
| New deps | k8s Lease API | none | k8s Lease API | memberlist/ckit + gossip mTLS | none |
| Non-k8s | not supported (v1) | n/a | not supported | yes | yes |
| Helm delta | small | medium | medium | large | trivial |
| Effort | **M** | M–L | L | XL | S (but wrong) |

### 5. Recommendation and phasing

**Build A, architected as lease-scoped work units; hold C as the demand-gated follow-on; do not build B, D, or E.** The dominant deployment is one tailnet, where the real HA demands are "a node failure shouldn't need a human" and "an upgrade shouldn't drop polls" — A delivers both for M effort. Some exclusivity mechanism is mandatory in every viable option (no backend OTLP dedup); the k8s Lease is the cheapest correct one. C shares A's machinery almost entirely, so the lease-per-work-unit shape keeps C incremental instead of a rewrite.

1. **Phase 1 (A1):** `coordination.mode: kubernetes`, whole-process lease, exit-on-demote, standby-not-ready gating, Helm `replicaCount <= 3` behind the flag, RBAC, leader-identity self-obs metric + status-page line. Per-replica state; document the failover duplicate window as an extension of the existing at-least-once contract.
2. **Phase 2 (A2):** `checkpoint.store: kubernetes` (batched ConfigMap writes, resourceVersion-guarded); switch the chart to RollingUpdate.
3. **Phase 3 (C), demand-gated:** per-tailnet leases, per-runtime start/stop lifecycle, stable instance-id base, receiver-routing decision.

### 6. Open decisions for the owner

1. **client-go vs minimal Lease client** — battle-tested `leaderelection` vs a heavy dependency tree on a lean static binary; a hand-rolled client fits repo style (`internal/hsapi` precedent) but re-implements subtle election edge cases. Middle path: vendor the election algorithm against a thin REST shim.
2. **Demote behavior:** exit(0)-and-restart (simple, recommended) vs in-process stop/start of the runtime set (needed anyway for phase 3 — pay now or later).
3. **Apiserver-outage stance:** step down (gap, no split-brain — recommended) vs keep-emitting grace period (double-emission risk).
4. **Checkpoint backend for phase 2:** ConfigMap vs Lease annotations vs "per-replica PVC + bounded cold-start is fine" (skipping phase 2 is defensible).
5. **Instance identity across failover:** per-pod instance ids (default, safe, series churn on failover) vs stable configured id (continuous series but collision risk during handover overlap — not recommended).
6. **Non-k8s posture:** explicitly singleton-only outside k8s, or commit to a generic lock backend later.
7. **Should any multi-replica mode hard-refuse `source: both`?** (cross-source dedup cannot work across processes; today only discouraged).
8. **Chart guard evolution:** `replicaCount must be 1` becomes "must be 1 unless coordination.mode=kubernetes" — the values schema max (`values.yaml:87`) must move in the same commit.

## Direction chosen by the owner, 2026-09-01

**Option A, both phases (A1 and A2).** Per-tailnet sharding (option C) stays a demand-gated follow-on and is not being built. Options B, D and E are rejected on the analysis above and should not be re-proposed without a new fact.

Answers to the open decisions in section 6:

1. **Lease client: use client-go leaderelection.** The owner accepted the k8s.io/client-go dependency tree rather than hand-rolling the renew/renew-deadline/steal semantics. Do not reimplement the election algorithm. Keep the dependency confined to the coordination package so nothing else in the binary reaches for client-go, and check what it does to binary size and to govulncheck's surface as part of the work.
2. **Demote behavior: exit(0) and let the kubelet restart the pod.** Phase 3 would need in-process runtime stop/start, but phase 3 is not being built.
3. **Apiserver-outage stance: step down.** An emission gap is preferred over any split-brain risk. Make the gap observable rather than mitigating it.
4. **Checkpoint backend: phase A2 is in scope**, so per-replica-PVC-and-live-with-it is off the table. The ConfigMap-versus-Lease-annotation choice is left to the implementing lane on the evidence.
5. **Instance identity: per-pod instance ids** stay the default. Series churn on failover is accepted; a stable configured id is not.
6. **Non-k8s posture: explicitly singleton-only outside Kubernetes** for this work. No generic lock backend.
7. **source: both under multi-replica:** left to the implementing lane, with the bar being that cross-source dedup provably cannot work across processes, so silence is the wrong answer - either refuse it or warn loudly at startup.
8. **Chart guard:** replicaCount must be 1 unless coordination.mode=kubernetes, and values.yaml's schema max moves in the same commit.

Release context that bounds the work: stable is v4.0.1, three waves sit unreleased, and the owner is deliberately still holding the release. Nothing here may cut a tag. Separately, the owner's standing plan is to drain the whole board and then cut v5 in one big bang, merging that release PR by hand - so a breaking change is not available to this work either.

Implementation is split into TSO-0107 (coordination core, A1), TSO-0108 (Kubernetes checkpoint store, A2) and TSO-0109 (Helm chart, RBAC and rollout).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Research and design task. Produced a five-option HA proposal with a state inventory, prior-art digest of Alloy clustering, k8s leader election and the otel target allocator, a comparison table and a flat recommendation. The owner chose Option A phases A1 and A2 with client-go leaderelection, and answered all eight open decisions; those answers are recorded in the notes above. Verified by the decision being recorded before any implementation split, which is what acceptance criterion 3 asks for. No code changed, so the gate is unaffected. Implementation continues as TSO-0107, TSO-0108 and TSO-0109.
<!-- SECTION:FINAL_SUMMARY:END -->
