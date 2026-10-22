---
id: TSO-0200
title: >-
  Stop restart and failover replay from sending log records Loki rejects as too
  old
status: Done
assignee:
  - '@claude'
created_date: '2026-10-22T00:53:00Z'
updated_date: '2026-10-22T01:14:00Z'
labels: []
dependencies: []
references:
  - internal/collector/keys/keys.go
  - internal/ingresswal/wal.go
  - internal/app/ingresswal.go
priority: high
type: bug
ordinal: 202000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The lab deployment fired the advisory 'Ingress permanently rejected data lost' (signal=logs) after the 5.2.0 to 5.2.1 rollout. Grafana Cloud Loki rejects entries older than its 7-day acceptance window with HTTP 400, and the exporter dropped the batches after ingress_wal.max_permanent_rejections. Two independent sources: (1) the keys collector keeps its 'lifecycle event already emitted' state in memory only, so every restart re-emits tailscale.key.created for every key with the key's original creation time as the log timestamp (keys created months ago are rejected; seen on every restart since at least 10-15). (2) A former leader's ingress WAL kept accepted receiver bodies for about 10 days while the pod sat as standby (a standby never replays and the new leader cannot read its volume); on re-promotion it replayed that backlog and Loki rejected it. The WAL is a short-horizon durability buffer for work ahead of delivery, not a historical store, so stale entries should be retired with explicit loss accounting instead of replayed. Owner chose this direction 2026-10-22.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Key created/revoked lifecycle events are not re-emitted after a restart when the semantic evidence store is durable
- [x] #2 A key lifecycle event whose source timestamp is older than the backend acceptance horizon is recorded as baseline instead of emitted, so a cold start never sends a record Loki rejects
- [x] #3 On startup and on every promotion, ingress WAL entries accepted longer ago than ingress_wal.max_replay_age are retired without replay through the durable completion path, counted per source and signal, and logged once at WARN; 0 disables expiry
- [x] #4 max_replay_age is validated, documented in docs/configuration.md, config.example.yaml and the Helm values, and the new counter appears in the metrics catalog and docs/metrics.md
- [x] #5 Unit tests prove each behaviour red before the fix and green after it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. keys: persist created/revoked source-timestamp watermarks in the evidence store (namespaced per tailnet in multi mode); skip events at or below the watermark or older than 6 days (baseline). 2. ingresswal.Store.ExpireBefore(ctx, cutoff): oldest-first, decode Accepted, retire via commitGeneration, skip prepared/completed, stop at first fresh entry; return per-route counts. 3. coordinator: optional expirer interface; ReplayStartup calls it first with now-max_replay_age; emit tailscale2otel.ingress_wal.expired.entries{source,signal} on the route provider and WARN once. 4. config key ingress_wal.max_replay_age (default 6h, 0 disables, >=0), docs, example, helm, regen. 5. just check, CodeRabbit (guarded: data deletion), commit+push.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Diagnosis (2026-10-22, lab): Loki usage-insights showed 'timestamp too old' for 4 key-created timestamps (2025-09-18..2026-06-06) on every restart since 10-15, plus ~600 entries from 2026-10-11 08:32 replayed when tailscale2otel-0 became leader at 23:50 with 50 stranded WAL entries (96 MB) accumulated while it flapped/sat as standby; 4 log batches were permanently dropped. Implemented: key lifecycle created/revoked watermarks in the semantic evidence store plus a 6-day baseline horizon; ingresswal.Store.ExpireBefore + coordinator ExpireStale run before every startup/promotion drain under ingress_wal.max_replay_age (default 6h), retiring via commitGeneration, skipping prepared/completed/unknown-route entries, counting tailscale2otel.ingress_wal.expired.entries{source,signal} and one WARN. Expired series added to the existing loss panel (dashboard query pin 241->242). Verification: red-then-green unit tests for keys restart + horizon, store expiry (durable across reopen, prepared/zero-cutoff/ineligible skip, eligibility negative-tested), coordinator accounting, startup regression; gaterun just check PASS (run 20261022T010622Z); just gen no diff; CodeRabbit complete with 0 findings.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Restarts and failovers no longer send log records Loki rejects as too old. Keys collector persists created/revoked watermarks in the evidence store and treats transitions older than 6 days as baseline. The ingress WAL retires entries accepted longer ago than ingress_wal.max_replay_age (new, default 6h, 0 disables) before each startup/promotion drain, with explicit loss accounting (tailscale2otel.ingress_wal.expired.entries + WARN). Documented the policy change (the WAL previously had no age-based cleanup). Risk: expiry is deliberate data loss for work older than the cap; a restart during a backend outage longer than 6h drops that backlog.
<!-- SECTION:FINAL_SUMMARY:END -->
