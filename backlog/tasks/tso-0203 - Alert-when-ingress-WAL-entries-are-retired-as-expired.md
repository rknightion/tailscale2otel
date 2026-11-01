---
id: TSO-0203
title: Alert when ingress WAL entries are retired as expired
status: Done
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:24:00Z'
labels:
  - alerts
dependencies: []
references:
  - deploy/alerts/gen/build_rules.py
priority: medium
type: feature
ordinal: 205000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TSO-0200 added tailscale2otel.ingress_wal.expired.entries, counting accepted work deliberately discarded past ingress_wal.max_replay_age. It is only on the loss panel; ts2o-ingress-wal-permanent-loss matches only permanent_drops_total. Generated through deploy/alerts/gen/build_rules.py.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A shipped Grafana-managed alert (and Prometheus rule export) fires when expired entries increase, with severity/domain labels per the alert taxonomy, generated via just gen
- [x] #2 Rule tests prove it fires on an increase and stays silent at zero; normalized Prometheus name used
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 expired alert lane: normalized rule with increase/zero promtool proofs, generated outputs root-owned.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landed09b22803aae07c82b8f9bb16a6afe6a7da6d6e91; combined artifacts in9f2abc6efa1283270cbc7b1774d04aa46e9cc85d. Promtool increase/zero/absence proof, CodeRabbit/dsreview and independent guarded PASS, exact composed gate20261101T211921Z-tailscale2otel-8f1b2d1c exit0. Grafana range/last retains recent positive longer than Prometheus5m; no identical recovery claim. Live publication readback pending separately.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped advisory expired-ingress-WAL alert in both generated rule formats with real firing fixtures.
<!-- SECTION:FINAL_SUMMARY:END -->
