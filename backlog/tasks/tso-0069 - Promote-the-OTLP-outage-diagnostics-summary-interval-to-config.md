---
id: TSO-0069
title: Promote the OTLP-outage diagnostics summary interval to config
status: Done
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-5
dependencies: []
priority: low
ordinal: 72000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The outage re-summary cadence is a hardcoded 5m with a comment saying "Not configurable (yet)" (internal/telemetry/delivery.go:141-144). Promote it to a validated config key with the current default.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The interval is a config key with default 5m and sane validation bounds
- [x] #2 Schema/docs regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes the delivery outage summary interval at the current 5m default with validation; lane H later wires it.

Lane H consumes the frozen outage_summary_interval key for exporter outage diagnostics and focused timing tests.
<!-- SECTION:PLAN:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Promoted the OTLP outage diagnostic summary interval to validated config with the 5m default and regenerated the schema and documentation surfaces. Implementation SHA f35b6ab. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
