---
id: TSO-0066
title: Per-tailnet cardinality limit overrides
status: To Do
assignee: []
created_date: '2026-08-30 09:31'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-1
dependencies: []
priority: high
ordinal: 69000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
cardinality.metric_limit and the warning/critical thresholds are process-global; in MSP mode one noisy tailnet forces raising the ceiling for all. Design decided (owner, 2026-08-30): optional per-entry limit in the tailnets: list, falling back to the global value - fits the per-tailnet Provider structure (internal/telemetry/providerset.go). Overflow/limits accounting and self-obs must stay per-tailnet attributable.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A tailnets: entry can carry its own metric limit and thresholds, defaulting to global
- [ ] #2 Overflow behaviour and self-obs metrics are attributable per tailnet
- [ ] #3 Config schema/docs regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
