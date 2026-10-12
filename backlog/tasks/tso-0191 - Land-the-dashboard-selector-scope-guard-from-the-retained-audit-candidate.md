---
id: TSO-0191
title: Land the dashboard selector-scope guard from the retained audit candidate
status: Done
assignee:
  - '@loop30'
created_date: '2026-10-12 19:03'
updated_date: '2026-10-12 20:19'
labels: []
dependencies: []
references:
  - deploy/grafana/gen
  - reference/grafana-delivery.md
priority: high
type: bug
ordinal: 191000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Split out of TSO-0172 (comprehensive dashboard audit) after loop28 and loop29 landed nothing: the audit's static, locally provable half was held hostage to its live-evidence half. TSO-0170 found a whole tab ignoring the tailnet and provider variables; the loop28 audit lane built a generated-dashboard selector-scope guard plus the generator fixes and regenerated both dashboards, with the full gate green and an independent security review passed, but it was never landed. The retained staged candidate lives in the gitignored codex/loop28 campaign directory (selector_scope.py, test_selector_scope.py, build.py, two existing test expectation lines and both generated dashboard JSONs) on a base that main has since moved past with dependency bumps only. Live per-panel verdicts and query evidence stay in TSO-0172.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every metric, log and trace query on every tab of both generated dashboards honours every dashboard variable that applies to it (tailnet, provider and any tab-local selector), enforced by a generated-dashboard test across all tabs, not only Policy and Config
- [x] #2 That test is seen failing on a deliberately unscoped query before it passes, and the failing output is recorded in the task notes
- [x] #3 Existing deploy/grafana/gen test expectation strings change only by adding the tailnet matcher; every other existing assertion is unchanged
- [x] #4 Both generated dashboard JSONs are regenerated from the generator with no hand edits, and the landed commit's ci-success is green with grafana-sync delivering both dashboards
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Rebase the retained selector-scope candidate in an isolated worktree; regenerate, reproduce the unscoped-query failure, run G, obtain fresh security review then sharded CodeRabbit; root lands and verifies composed gate, exact-SHA CI and dashboard delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop30: 2 implementation attempts, 1 review-repair round. Initial gate passed but fresh security review found a mixed scheduler TraceQL collector false pass; retry corrected conditional scope and independent assembled/shipped behavioral negative tests, then full gate and delta security passed. Original unscoped ratio proof failed with missing tailnet/provider; mixed-trace proof failed before correction. Existing test hunks add only tailnet matcher. CodeRabbit completed with zero major/critical and one out-of-scope historical audit-plan minor; actual completion listed all candidate Python files and both artifacts. Land 00a9936814845d5f46c4beacc410b65ed8536e0d; fresh detached composed G exit 0; CI 38302365105 ci-success success; grafana-sync 38302365172 success; both far-side dashboard blobs match landed commit. helm-success absent, not a pass. Backend TraceQL parser/Grafana interpolation remains unverified; later live audit owns that evidence.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed selector scope guard and regenerated both dashboards at 00a9936814845d5f46c4beacc410b65ed8536e0d. Full local/composed gates, fresh guarded review and exact CI passed; both GitSync artifacts verified by blob identity. Guard now detects the previously missed mixed scheduler query as well as unscoped metric operands.
<!-- SECTION:FINAL_SUMMARY:END -->
