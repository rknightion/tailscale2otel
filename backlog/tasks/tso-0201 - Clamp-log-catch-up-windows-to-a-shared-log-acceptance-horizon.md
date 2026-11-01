---
id: TSO-0201
title: Clamp log catch-up windows to a shared log-acceptance horizon
status: Done
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:58:00Z'
labels:
  - guarded
  - data-loss
dependencies: []
references:
  - internal/collector/window.go
  - internal/collector/keys/keys.go
  - config.example.yaml
priority: high
type: bug
ordinal: 203000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
nextWindow (internal/collector/window.go) resumes from a checkpoint of any age and walks forward in max_window chunks with no age limit, so after a long outage or a promotion onto a stale checkpoint the audit, flow and object-store collectors emit records with source timestamps older than the log backend accepts (Loki rejects beyond about 7 days); those batches end in permanent drops. config.example.yaml advertises an object-store initial_lookback up to 14 days. TSO-0200 (stop replay sending logs Loki rejects as too old) fixed only the keys collector and the ingress WAL; keys.go still hardcodes lifecycleEventHorizon = 6d. Owner ruling 2026-11-01: clamp and count, one shared config key delivery.log_max_record_age (default 6d) that also replaces lifecycleEventHorizon.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 With a checkpoint older than delivery.log_max_record_age, the next window starts at now minus the horizon, no record older than the horizon is emitted, and the skipped span is counted in a self-obs counter with the collector as an attribute plus one WARN log
- [x] #2 initial_lookback larger than the horizon is clamped (Warnings() flags it) for poll and object-store paths
- [x] #3 keys.go uses the shared key instead of lifecycleEventHorizon with unchanged default behaviour; 0 disables the clamp everywhere
- [x] #4 The key is in config.schema.json, Helm values/schema, the env loader, docs/configuration.md and config.example.yaml; the new counter is in the catalog with a disposition
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 horizon lane: shared key and clamp/count semantics across ingestion paths, config seams, gate and review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landed5355f59d5f77ccc2235eeb3532f6ffd33ec8c828 plus reviewed selector repair1d321fec55ac8f6c8e499db94be7f961db714117. Shared6d/0-disable key wired across poll/object-store/keys, source-timestamp log fence and skipped-duration metric/WARN; expired gaps and saved partitions excluded before ingestion/prefix budgets. D24 independent PASS, shard-scoped CR falsepositives source-proven; baseline flat metadata paging tracked separately as TSO-0210 (large flat archive freshness design). Maintained YAML imports and existing golden rows preserved; only144h default/provenance rows added. Final exact composedgate20261101T215433Z-tailscale2otel-78de40be exit0, full regeneration/no-diff. Absolute+rate panels retain firstpositive startup sample and honorcollector control.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped the shared log acceptance horizon with clamp/count semantics, bounded old-work admission and complete config/generated surfaces; final exact gate passed.
<!-- SECTION:FINAL_SUMMARY:END -->
