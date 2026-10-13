---
id: TSO-0184
title: >-
  Emit tailnet ownership on runtime traces before offering tailnet-scoped trace
  panels
status: Done
assignee: []
created_date: '2026-10-11 12:09'
updated_date: '2026-10-13 22:59'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: task
ordinal: 184000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
app.New passes a.tracer (process tracer) to runtime clients/schedulers and receivers. These spans do not carry a runtime tailnet dimension; adding a query matcher would silently hide valid spans. Only the emitted scrape collector dimension is repaired in this candidate. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Two-runtime local trace-export fixture proves scheduler/API/node/receiver spans carry the correct tailnet ownership where applicable.
- [x] #2 Process-global release-check spans remain explicitly process-global.
- [x] #3 Trace panels honor a non-All tailnet selection without mixing runtime ownership or erasing valid process traces under All.
- [x] #4 Root verifies both All and selected-tailnet TraceQL against bounded live evidence after delivery.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wire owning provider tracers into runtime APIs, schedulers, node discovery and routed receivers; preserve process-global releases. Validate actual OTLP export, then final native and delivered live queries.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner commissioned ordinary work on main on 2026-10-13. Root owns integration, delivery and final proof; bounded workers own disjoint source/evidence paths. Replay remains blocked.

Actual two-runtime OTLP fixture and negative/race tests pass. Native final union render passes. Delivered live query matches exact six All trace IDs and two per owner; process traces preserved only under All. Evidence: /Users/rob/repos/tailscale2otel/codex/dashboard-closeout/live/final-query-proof.json and /Users/rob/repos/tailscale2otel/codex/dashboard-closeout/browser/trace-render-final-union/verdict.json. Full just check passed on final source; generation clean; justfile formatting gate passed, no new recipes. Source commit 760fda5e7c725ae2e1f6499a77bf681bfcd49585; exact-source CI and GitSync succeeded. CodeRabbit Go shard completed with zero findings; Python shard failed No files to review and is not claimed clean.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Runtime ownership emitted and trace selectors verified locally and after GitSync delivery.
<!-- SECTION:FINAL_SUMMARY:END -->
