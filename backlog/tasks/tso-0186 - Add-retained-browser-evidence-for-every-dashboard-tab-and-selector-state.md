---
id: TSO-0186
title: Add retained browser evidence for every dashboard tab and selector state
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
ordinal: 186000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Both whole-dashboard snapshots succeeded but selected Overview only; 488 panels have no visible render evidence. Query receipts cannot validate transformations, plugins, row gates or actual Grafana ad-hoc filtering. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Drive each of the 22 leaf tabs and capture panel identities, rendered errors, empty states and feature-gate outcomes under a bounded fixture window.
- [x] #2 Exercise dashboard, tab-local and ad-hoc selectors with non-All values and assert isolation where the signal carries the dimension.
- [x] #3 Retain screenshots/structured receipts outside tracked source and distinguish dense-render limitations from panel failures.
- [x] #4 Use a local/fake backend fixture by default; live reads only when explicitly authorized.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Exercise all tabs and supported controls in native local Grafana with real backend software and synthetic fixtures; retain receipts and screenshots. Re-render changed trace panels and bind unchanged panel specs.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner commissioned ordinary work on main on 2026-10-13. Root owns integration, delivery and final proof; bounded workers own disjoint source/evidence paths. Replay remains blocked.

22 leaf tabs, 88 logical selector states, 514 panel identities, 637 contract-qualified isolation checks with zero leaks, and 58 control receipts. Final source equivalence binds 509 unchanged panels and five separately re-rendered trace panels. Failed attempts and resource limits retained. Evidence: /Users/rob/repos/tailscale2otel/codex/dashboard-closeout/browser/final-source-equivalence.json and /Users/rob/repos/tailscale2otel/codex/dashboard-closeout/browser/assessment-health-native-url-05/summary.json. Live reads were authorized for separate runtime trace acceptance. Full just check passed on final source; generation clean; justfile formatting gate passed, no new recipes. Source commit 760fda5e7c725ae2e1f6499a77bf681bfcd49585; exact-source CI and GitSync succeeded. CodeRabbit Go shard completed with zero findings; Python shard failed No files to review and is not claimed clean.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Retained complete tab/panel/control browser evidence using local synthetic fixtures and actual backend services.
<!-- SECTION:FINAL_SUMMARY:END -->
