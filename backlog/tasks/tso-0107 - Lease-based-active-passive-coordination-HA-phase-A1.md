---
id: TSO-0107
title: Lease-based active-passive coordination (HA phase A1)
status: Done
assignee: []
created_date: '2026-09-01 20:01'
updated_date: '2026-09-01 23:37'
labels: []
milestone: m-10
dependencies: []
priority: high
type: feature
ordinal: 108000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TSO-0033 chose Option A: k8s Lease active-passive failover with a whole-process leader. This is phase A1, the coordination core, and it owns the config seam every other HA task codes against - freeze the config keys and the package surface before TSO-0108 and TSO-0109 build on them.

Every replica starts, loads config, builds providers and serves the liveness probe. Only the lease holder runs the runtime set: schedulers, receivers, ingress WAL replay, rollup flusher, heartbeat. A standby campaigns for the lease and reports NOT ready, so the Service routes receiver and admin traffic to the leader alone. On losing leadership the process exits 0 and the kubelet restarts it. On gaining leadership it does a normal startup, which already replays the WAL before listeners open.

Use client-go's leaderelection package. This is the owner's decision and it is not open for relitigation: do not hand-roll the renew, renew-deadline and steal semantics. Confine k8s.io/client-go to the new coordination package so nothing else in the binary reaches for it, and report what it does to binary size and to the govulncheck surface.

On apiserver unavailability the leader steps down at the renew deadline. That emission gap is deliberate and is preferred over any split-brain risk - make it observable, do not mitigate it. Instance identity stays per-pod, so series churn across failover is expected and correct. Outside Kubernetes the exporter is explicitly singleton-only; do not build a generic lock backend.

Failover duplication is a bounded extension of the existing at-least-once contract, not a new violation: the scheduler already advances checkpoints only on success and ReplayOverlap already re-reads deliberately. Document it as such. Without TSO-0108 a failover cold-starts cursors and re-emits up to initial_lookback, which is the A1 baseline and is acceptable.

Decide and record: whether a multi-replica configuration should refuse flowlogs or auditlogs source: both, given cross-source dedup provably cannot work across processes. Silence is the wrong answer - either refuse it in Validate or warn loudly at startup.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 config exposes coordination.mode with none as the default and kubernetes as the only other value, plus lease name, namespace and the lease-duration, renew-deadline and retry-period timings, all validated
- [x] #2 a standby replica reports not ready on the readiness probe and starts no scheduler, receiver, WAL replay, rollup flusher or heartbeat
- [x] #3 losing leadership exits the process with status 0, and gaining it performs the normal startup sequence with WAL replay ahead of listeners
- [x] #4 a leader-identity self-observability metric and a status-page line make the current holder and the stepped-down state visible
- [x] #5 an apiserver outage steps the leader down rather than continuing to emit, and the resulting gap is observable
- [x] #6 k8s.io/client-go is reachable only from the coordination package, and the binary-size and govulncheck impact is recorded on the task
- [x] #7 the source: both under multi-replica question is decided, implemented and recorded
- [x] #8 coordination.mode none behaves exactly as today, proven by the existing suite staying green with no coordination config present
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Freeze coordination and checkpoint config keys first; implement client-go leader election and process lifecycle wiring; add observability and focused tests; report dependency, binary-size, and vulnerability impact.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Wave 5 decision: coordinated mode accepts flowlogs source=both but emits a loud startup warning; active-passive still prevents concurrent emitters, while validation refusal would reject an otherwise supported ingestion topology. client-go remains confined to internal/coordination. The final binary is 90,428,050 bytes versus the 41,840,658-byte baseline: +48,587,392 bytes, or 116.1%. Govulncheck found no reachable vulnerabilities in any of the five modules, and the locked OTEL core/log SDK versions did not move.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented Lease-based active-passive coordination in 1195f4b with integrated leader/status observability at 48bf65c8bf30c0f77f679728b4b56947bd5df944. Focused lifecycle and outage tests, negative-tested guards, two complete five-shard CodeRabbit reviews, repeated just check passes, and exact-head CI run 33569379997 success prove the config, standby, gain/demotion, step-down, observability, dependency-boundary, warning, and singleton contracts.
<!-- SECTION:FINAL_SUMMARY:END -->
