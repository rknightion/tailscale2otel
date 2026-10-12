---
id: TSO-0182
title: >-
  Count distinct metric families rather than reporter series in the family-count
  tile
status: Done
assignee:
  - '@loop30'
created_date: '2026-10-11 12:09'
updated_date: '2026-10-12 22:10'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 182000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The description promises distinct metric families but count(tailscale2otel_series_active) counts reporter time series; multiple replicas/runtimes can repeat metric_name. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A fixture with two reporters for one metric_name yields one distinct family.
- [x] #2 Adding a second distinct metric_name increases the count to two.
- [x] #3 Respect selected tailnet scope and document whether the tile is global or per selected runtime; preserve series-cost sum as a separate question.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Correct family count to distinct metric_name across reporters, preserve separate series-cost sum and selected scope; actual local PromQL fixture proves one/two families and wrong-tailnet exclusion with baseline red; regenerate/stage, serialized G, guarded review/CodeRabbit then root composed/CI/delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

loop30: 1 implementation attempt, no review repair. Family tile groups actual metric_name before outer count, not reporter series. Actual engine fixture failed before and passes one/two family plus selected-tailnet/All/process/empty cases; separate series-cost sum unchanged. Fresh guarded review every Python/JSON hunk and engine-negative/green identity passes. Full candidate G exit0; CodeRabbit ran once No files to review exit1, two Python files unreviewed/not clean under D69 separate review coverage. Land 01f65be9b59adc44c49f556e0f37c07989046308; fresh detached composed G exit0; CI 38314125633 ci-success 115029438479 success; grafana-sync 38314125656 success, exact far-side health blob matches. helm-success absent. No latest-state inventory/ad-hoc browser/backend claim. Evidence /Users/rob/repos/tailscale2otel/codex/loop30/tso0182/.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed distinct reported metric-family count at 01f65be9b59adc44c49f556e0f37c07989046308. Selected-tailnet union and All/unlabeled-process semantics documented; cost sum remains separate. Exact local engine/gates/CI/GitSync proofs complete.
<!-- SECTION:FINAL_SUMMARY:END -->
