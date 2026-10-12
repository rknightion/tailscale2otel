---
id: TSO-0180
title: >-
  Make absent runtime summary data visually distinct from healthy process
  telemetry
status: Done
assignee:
  - '@loop30'
created_date: '2026-10-11 12:09'
updated_date: '2026-10-12 21:48'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 180000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Exporter Health Overview renders No data in green for both process gauges in the current window. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Absent runtime series renders neutral or clearly missing-source text rather than healthy-colored No data.
- [x] #2 A present numeric runtime sample keeps its intended unit and interpretation.
- [x] #3 Capture both outcomes in a browser/render fixture.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Freeze runtime summary tile inventory; implement neutral missing-source semantics preserving numeric units; prove actual Grafana fixture renders for absent/numeric data using prior ignored local harness; regenerate and stage candidate, serialized G, guarded review then CodeRabbit and root composed/CI/delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

loop30: 1 implementation attempt, no review repair. Three exact Runtime stat tiles show gray No process telemetry in range while numeric expressions/units/layout/polarity remain unchanged. Actual Grafana13/Chromium renders prove missing vs 42/8/1-hour synthetic values with three generated query expressions/state; rejected zero-query and startup failures not proof. Full candidate G exit0, fresh guarded review all Python/fixture/gate identities passes. CodeRabbit ran once but No files to review exit1; two Python files unreviewed, not clean, D69 separate security coverage. Land 6f1797baf6a58b68e75194192f74d18e704e4797; fresh detached composed G exit0; CI 38311465676 ci-success 115021006060 success; grafana-sync 38311465801 success and exact far-side health blob matches. helm-success absent. No full-tab navigation/live datasource, null/NaN/denied-datasource or final all-clear console claim. Evidence /Users/rob/repos/tailscale2otel/codex/loop30/tso0180/.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed neutral missing process telemetry formatting at 6f1797baf6a58b68e75194192f74d18e704e4797. Exact local render/regression, candidate/composed gates and CI/GitSync verified; numeric runtime interpretation unchanged.
<!-- SECTION:FINAL_SUMMARY:END -->
