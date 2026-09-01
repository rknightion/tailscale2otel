---
id: TSO-0105
title: Grafana alert rules remain unevaluated after publication
status: Done
assignee: []
created_date: '2026-09-01 17:46'
updated_date: '2026-09-01 23:37'
labels: []
milestone: m-10
dependencies: []
priority: high
type: bug
ordinal: 106000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live Wave 3 validation read the alerting engine state directly and found many shipped, unpaused tailscale2otel alert rules with a zero last-evaluation timestamp. Their default inactive/ok fields do not prove evaluation, so these rules currently provide no demonstrated protection. Recording rules and a subset of alerts do evaluate, which rules out a stack-wide scheduler outage. Diagnose the per-rule scheduling or resource-shape difference; do not treat publication success or health=ok as evaluation evidence.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every shipped unpaused alert rule acquires a nonzero recent last-evaluation timestamp after publication
- [x] #2 Rules whose feature is disabled evaluate to the configured no-data disposition or are deliberately and visibly paused
- [x] #3 A verification check fails when any shipped unpaused rule retains a zero last-evaluation timestamp
- [x] #4 Live read-back records state, health, last evaluation, and last error for every shipped rule
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement a fail-closed post-publication evaluator that inventories all shipped unpaused rules and requires recent nonzero evaluations with state, health, and error read-back; negative-test the guard; validate with a real Grafana push/read-back if manifests change.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up direct engine read-back at 2026-09-01 19:08Z found all 44 unpaused ts2o rules with nonzero recent lastEvaluation timestamps. The earlier zero-timestamp observation was transient after publication/rollout rather than persistent. Keep this task for a fail-closed publication verifier so the transient window cannot be mistaken for either a clean pass or a lasting scheduler fault.

The authorized Grafana publication wrote 126 resources with zero failures. Direct runtime read-back after the publication boundary recorded all 125 shipped rules completely; all 44 shipped unpaused alert rules had recent nonzero evaluations, disabled-feature rules matched their configured no-data or pause contract, and missing and failure sets were empty. Grafana omits an empty lastError field; the verifier normalizes that omission only for an otherwise complete Prometheus runtime response shape.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented the fail-closed evaluation verifier in eb5c7fd and corrected the live runtime response shape in f2e221e, integrated at 48bf65c8bf30c0f77f679728b4b56947bd5df944. Negative tests prove zero/stale evaluations and incomplete records fail. Real push plus direct read-back passed, as did 127 alert tests, full just check, and exact-head CI 33569379997.
<!-- SECTION:FINAL_SUMMARY:END -->
