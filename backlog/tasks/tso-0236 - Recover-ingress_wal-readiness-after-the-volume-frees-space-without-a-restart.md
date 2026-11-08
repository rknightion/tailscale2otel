---
id: TSO-0236
title: Recover ingress_wal readiness after the volume frees space, without a restart
status: To Do
assignee: []
created_date: '2026-11-08T13:55:00Z'
labels:
  - bug
dependencies: []
priority: high
ordinal: 238000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
On 2026-11-08 the leader's 20Gi claim filled (first sweep after cutting flows.store.retention 720h to 240h: the DELETE grew the SQLite WAL). The claim was expanded online within ~8 minutes, but /readyz kept returning 503 'ingress_wal: failed' for 47 minutes until the pod was restarted. The leader-only streaming Service therefore had no ready endpoint and Tailscale's pushed flow/audit logs could not be delivered for that whole time. Expected: the WAL component retries and returns to healthy once writes succeed again, or the process exits so kubelet restarts it. Related, from the same incident: a sweep DELETE of ~2/3 of an 18G table in one statement is both slow and WAL-heavy; consider batched deletes (bounded LIMIT per transaction) so a retention cut cannot fill the disk or exceed query_timeout.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 After a transient ENOSPC that clears, readiness recovers without a restart (test reproduces the stuck state first)
- [ ] #2 Retention sweep deletes in bounded batches so one sweep's WAL growth is capped
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
