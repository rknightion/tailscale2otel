---
id: TSO-0108
title: Kubernetes checkpoint store backend (HA phase A2)
status: Done
assignee: []
created_date: '2026-09-01 20:02'
updated_date: '2026-09-01 23:37'
labels: []
milestone: m-10
dependencies:
  - TSO-0107
priority: medium
type: feature
ordinal: 109000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TSO-0033 phase A2. Without it, an A1 failover cold-starts poll cursors and re-emits up to initial_lookback of logs. With it, a new leader resumes cursors near-seamlessly.

CheckpointStore in internal/collector/checkpoint.go:31-38 is four methods over a small JSON payload, so a second backend is a contained change. Add checkpoint.store with file as the default and kubernetes as the new value, writing the cursor map to the apiserver.

Two hard constraints. First, persistLocked currently rewrites the whole map on every Set - do not put that write rate against the apiserver. Coalesce writes, roughly one every five seconds, and flush on shutdown. Second, guard updates with resourceVersion so a deposed leader's late write fails loudly instead of clobbering the new leader's cursors; that failure is a correctness signal and must be surfaced, not swallowed.

ConfigMap versus Lease annotations is the implementing lane's call on the evidence - weigh the 1MiB ConfigMap ceiling against annotation size limits at the real cursor-map size for a multi-tailnet deployment, and record which you picked and why.

A shared RWX volume is NOT an acceptable alternative and must not be introduced: two processes sharing the checkpoint file clobber each other's keys even for disjoint tailnets, because the whole map is rewritten on every write.

Also in scope: switch the chart to RollingUpdate now that a new pod blocks on the lease, which removes upgrade downtime - the more common outage than a node failure. Coordinate that edit with TSO-0109 so the deployment template has one owner.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 checkpoint.store selects between the file backend, which stays the default and unchanged, and a new kubernetes backend
- [x] #2 writes are coalesced rather than issued per Set, and a shutdown flushes the pending map
- [x] #3 updates are resourceVersion-guarded, and a deposed leader's stale write fails visibly instead of overwriting current cursors
- [x] #4 a failover under the kubernetes backend resumes cursors without re-emitting the initial_lookback window, proven by test
- [x] #5 the ConfigMap-versus-annotations choice is recorded with the size evidence that decided it
- [x] #6 the chart uses RollingUpdate when coordination is enabled, with the deployment template edited by exactly one of this task and TSO-0109
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
After C freezes checkpoint.store, implement a coalesced resourceVersion-guarded Kubernetes checkpoint backend with shutdown flush; choose ConfigMap or Lease annotations from size evidence; prove cursor continuity in tests.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Wave 5 chose a dedicated ConfigMap rather than Lease annotations: ConfigMap data allows 1,048,576 bytes versus the 262,144-byte aggregate annotation ceiling, while the representative two-tailnet ordinary cursor map is 196 bytes. Synthetic configured bounds are 15,859,713 bytes for replay state and 1,680,001 bytes for object-store seen state; the backend therefore rejects oversize maps visibly and never truncates, and TSO-0110 tracks the scalable high-cardinality follow-up. The ConfigMap name is derived as lease-name-checkpoints.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented the coalesced resourceVersion-guarded Kubernetes checkpoint backend in ed38f70, with shutdown flush, stale-leader conflict visibility, default file-backend compatibility, and failover cursor-continuity tests. aea526d supplies the coordinated RollingUpdate chart seam. Negative-tested guards, full just check, and exact-head CI 33569379997 passed at integrated head 48bf65c8bf30c0f77f679728b4b56947bd5df944.
<!-- SECTION:FINAL_SUMMARY:END -->
