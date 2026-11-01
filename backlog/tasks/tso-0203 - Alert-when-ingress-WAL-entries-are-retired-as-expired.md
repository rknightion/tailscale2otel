---
id: TSO-0203
title: Alert when ingress WAL entries are retired as expired
status: In Progress
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:09:00Z'
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
- [ ] #1 A shipped Grafana-managed alert (and Prometheus rule export) fires when expired entries increase, with severity/domain labels per the alert taxonomy, generated via just gen
- [ ] #2 Rule tests prove it fires on an increase and stays silent at zero; normalized Prometheus name used
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 expired alert lane: normalized rule with increase/zero promtool proofs, generated outputs root-owned.
<!-- SECTION:PLAN:END -->
