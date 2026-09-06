---
id: TSO-0142
title: Emit the modeled capability scope preflight metric at runtime
status: Done
assignee:
  - codex-root
created_date: '2026-09-05 22:01'
updated_date: '2026-09-06 10:30'
labels: []
dependencies: []
references:
  - internal/app/app.go
  - internal/app/capability.go
  - internal/app/capability_test.go
priority: medium
type: bug
ordinal: 143000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The shipped catalog and dashboard expose `tailscale2otel.capability.scope_satisfied`, and the live admin capability matrix reports modeled OAuth scopes as satisfied, but a trailing 30-day service-scoped presence query found no series. Source inspection shows `EmitScopePreflight` is exercised by unit tests but has no production call site; the heartbeat callback only invokes `EmitCapabilityStatus`. Operators therefore cannot observe a real configured-scope regression through the shipped metric.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A running exporter with modeled OAuth scopes emits one bounded `tailscale2otel.capability.scope_satisfied` series per checkable capability, matching the admin capability matrix
- [x] #2 Unknown and not-applicable scope states remain absent rather than becoming false permission failures
- [x] #3 A runtime-level test fails when the production reporter stops invoking scope-preflight emission
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 15 contract: root-owned tracker and integration; flat independent lanes after a green baseline. Lane A uses EXECUTION, gpt-5.6-luna/max, self-contained, no delegation. Add only the heartbeat reporter call and a runtime Recorder regression test; observe the negative run; root regenerates, gates, reviews, commits with explicit paths and checks exact-head workflows. No live-system access. The current wave supersedes the older scheduling note.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner decision 2026-09-05: bundled into the 2026-09-11 wave with TSO-0133; PR #585 (5.0.0) merges after both land.

Phase-0 just check passed at baseline 9414328c573a21ce0aa73a69eecc0c54d980dbee (exit 0). Independent implementation lanes now start together; root owns full integration and final checks.

Lane A returned runtime regression evidence: newApp plus synctest drives initial heartbeat and one tick; omission of production call failed with an empty scope-preflight map; positive targeted, race and catalog-emit-site checks passed. Root review requests explicit fixture preconditions for satisfied, insufficient, unknown and not-applicable rows, plus map membership checking so a missing zero-valued point cannot be treated as present.

Delivered 86348164c736e2db82d745d773e0eed8900d9024: the production heartbeat computes one capability matrix and calls both reporters. Runtime test through newApp and synctest covers satisfied, insufficient, unknown and not-applicable rows, checks point membership and bounded values, and exercises initial emission plus one tick. The new call was explicitly removed by hand: the test failed with missing capability points; restoring it passed the targeted suite. Existing emitter semantics are unchanged. Full local just check passed at 5d966fe82759fb1a6c8fc9222b1b7d4e4e261133; just --fmt --check passed. Two full regenerations were byte-stable on the second; docs/metrics.md, the coverage manifest and the chart schema are unchanged. CodeRabbit completed with zero findings. Exact-code-head CI 34026607134, Helm 34026607302, Release 34026607341 and auto-rc 34026995398 all succeeded on attempt 1. RC v5.0.0-rc.31 resolves to that code commit. Known local skips: TestRuleCountsFromRealACL (fixture absent), TestDumpFlowsJSON (output not requested), TestDefaultCheckpointPath_XDGStateHomeWins (macOS). Live verify-deploy was not run because live access is excluded. The current grafana-sync workflow is path-filtered; this wave changes no matching path and triggered no Grafana write workflow. PR #585 remains owner-held.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Delivered 86348164c736e2db82d745d773e0eed8900d9024: the production heartbeat computes one capability matrix and calls both reporters. Runtime test through newApp and synctest covers satisfied, insufficient, unknown and not-applicable rows, checks point membership and bounded values, and exercises initial emission plus one tick. The new call was explicitly removed by hand: the test failed with missing capability points; restoring it passed the targeted suite. Existing emitter semantics are unchanged. Full local just check passed at 5d966fe82759fb1a6c8fc9222b1b7d4e4e261133; just --fmt --check passed. Two full regenerations were byte-stable on the second; docs/metrics.md, the coverage manifest and the chart schema are unchanged. CodeRabbit completed with zero findings. Exact-code-head CI 34026607134, Helm 34026607302, Release 34026607341 and auto-rc 34026995398 all succeeded on attempt 1. RC v5.0.0-rc.31 resolves to that code commit. Known local skips: TestRuleCountsFromRealACL (fixture absent), TestDumpFlowsJSON (output not requested), TestDefaultCheckpointPath_XDGStateHomeWins (macOS). Live verify-deploy was not run because live access is excluded. The current grafana-sync workflow is path-filtered; this wave changes no matching path and triggered no Grafana write workflow. PR #585 remains owner-held.
<!-- SECTION:FINAL_SUMMARY:END -->
