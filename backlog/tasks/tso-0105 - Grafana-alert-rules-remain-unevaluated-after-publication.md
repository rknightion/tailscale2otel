---
id: TSO-0105
title: Grafana alert rules remain unevaluated after publication
status: To Do
assignee: []
created_date: '2026-09-01 17:46'
updated_date: '2026-09-01 20:02'
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
- [ ] #1 Every shipped unpaused alert rule acquires a nonzero recent last-evaluation timestamp after publication
- [ ] #2 Rules whose feature is disabled evaluate to the configured no-data disposition or are deliberately and visibly paused
- [ ] #3 A verification check fails when any shipped unpaused rule retains a zero last-evaluation timestamp
- [ ] #4 Live read-back records state, health, last evaluation, and last error for every shipped rule
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up direct engine read-back at 2026-09-01 19:08Z found all 44 unpaused ts2o rules with nonzero recent lastEvaluation timestamps. The earlier zero-timestamp observation was transient after publication/rollout rather than persistent. Keep this task for a fail-closed publication verifier so the transient window cannot be mistaken for either a clean pass or a lasting scheduler fault.
<!-- SECTION:NOTES:END -->
