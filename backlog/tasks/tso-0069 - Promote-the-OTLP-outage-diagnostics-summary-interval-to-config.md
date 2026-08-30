---
id: TSO-0069
title: Promote the OTLP-outage diagnostics summary interval to config
status: In Progress
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-30 23:22'
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
- [ ] #1 The interval is a config key with default 5m and sane validation bounds
- [ ] #2 Schema/docs regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes the delivery outage summary interval at the current 5m default with validation; lane H later wires it.
<!-- SECTION:PLAN:END -->
