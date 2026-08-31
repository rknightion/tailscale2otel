---
id: TSO-0068
title: Delta-temporality escape hatch for OTLP metrics export
status: Done
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-5
dependencies: []
priority: medium
ordinal: 71000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
cumulativeTemporalitySelector (internal/telemetry/exporters.go:51-57) is unconditional - correct for Grafana Cloud, but backends preferring delta have no knob. Keep cumulative as the default and add a config override (validated enum), documenting the Grafana Cloud guidance.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Temporality is configurable with cumulative default; docs state when to change it
- [x] #2 Config schema/env reference regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes OTLP metric temporality as cumulative by default with a validated enum; lane H later wires exporter selection and tests it.

Lane H consumes the frozen metric_temporality enum across both OTLP exporters, preserving gauge last-value semantics and testing delta versus cumulative selection.
<!-- SECTION:PLAN:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added configurable OTLP metric temporality with cumulative as the behavior-preserving default, regenerated schema and env documentation, and pinned the wire behavior with exporter tests. Implementation SHA f35b6ab. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
