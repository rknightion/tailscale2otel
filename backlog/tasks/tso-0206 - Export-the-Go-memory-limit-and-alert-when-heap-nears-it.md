---
id: TSO-0206
title: Export the Go memory limit and alert when heap nears it
status: Done
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:31:00Z'
labels:
  - alerts
  - dashboards
dependencies: []
references:
  - deploy/helm/tailscale2otel/values.yaml
priority: medium
type: feature
ordinal: 208000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
OOM restarts happened (TSO-0192, TSO-0197). The chart sets GOMEMLIMIT but no metric reports the effective limit and no runtime.memory family has an alert. Read the effective limit with debug.SetMemoryLimit(-1).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A self-obs gauge reports the effective Go memory limit (absent or a stated sentinel when unlimited), in the catalog with a disposition
- [x] #2 A dashboard panel on Exporter Health shows heap against the limit, and a shipped alert fires when heap stays above a stated fraction of a set limit, both generated via just gen
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 memory lane: effective memory limit gauge, panel and alert, gate and review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landedfdaa4d8b84c44cde5ca99c00f6f4256886a8b409. Effective Go limit gauge emits -1 when unlimited; paired heap panel/rule. Independent guarded review caught stale historical reduction; per-rule instant query repaired and proved in native Grafana12.3 fixture pipeline plus real PromQL transitions. Other rule objects preserved. Final independent PASS; exact composed gate20261101T212748Z-tailscale2otel-9e155cc2 exit0. Python CodeRabbit gaps independently reviewed; no Grafana13 live/scheduler claim.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Exported effective Go memory limit and generated heap-pressure view/alert, including finite-to-unlimited and zero transitions.
<!-- SECTION:FINAL_SUMMARY:END -->
