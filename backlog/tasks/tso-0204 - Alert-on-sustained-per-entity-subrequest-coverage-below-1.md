---
id: TSO-0204
title: Alert on sustained per-entity subrequest coverage below 1
status: Done
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:24:00Z'
labels:
  - alerts
dependencies: []
references:
  - internal/apistate/catalog.go
  - deploy/alerts/gen/build_rules.py
priority: medium
type: feature
ordinal: 206000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
tailscale2otel.subrequest.coverage and subrequest.failures (internal/apistate/catalog.go) are visualised but not alertable, so partial per-device posture or invite data degrades silently.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A shipped alert fires when coverage stays below 1 for a sustained window per collector/tailnet and resolves on recovery, generated via just gen
- [x] #2 Rule tests cover firing, a transient dip that must not fire, and recovery
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 coverage lane: sustained/transient/recovery alert behavior proven using promtool, generated outputs root-owned.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landed9f2abc6efa1283270cbc7b1774d04aa46e9cc85d. Sustained/transient/recovery/isolation fixtures pass; healthy1 survives Grafana last reduction before threshold. CodeRabbit/dsreview and independent guarded PASS; exact composed gate20261101T211921Z-tailscale2otel-8f1b2d1c exit0. Root output/count/disposition integration complete.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped sustained incomplete-subrequest coverage alert with recovery and transient suppression proof.
<!-- SECTION:FINAL_SUMMARY:END -->
