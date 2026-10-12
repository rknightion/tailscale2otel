---
id: TSO-0183
title: >-
  Remove fixed historical cap and live-population claims from cardinality panel
  descriptions
status: Done
assignee:
  - '@loop30'
created_date: '2026-10-11 12:09'
updated_date: '2026-10-12 22:54'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 183000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Descriptions assert a 10k cap, a fixed group population and a historical series population/normal overflowing state rather than the deployment's configured limit and current catalog. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Description text makes no fixed 10k cap assumption and refers to the exported configured limit, including unlimited mode.
- [x] #2 Group/family descriptions are portable across enabled collector sets and replicas, with no historical live population presented as a contract.
- [x] #3 Verify generated descriptions and unchanged query/signal coverage.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Replace fixed-cap/history-as-contract cardinality descriptions with configured-limit/unlimited and portable reporter/collector semantics; validate exact generated descriptions and unchanged query/signal inventory without incidental prose tests; regenerate/stage, serialized G, guarded review and goal CodeRabbit disposition then root composed/CI/delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

loop30: 1 worker implementation attempt and 1 minor prose review-repair round. Five generated descriptions remove fixed historical cap/population assumptions and distinguish configured positive limit/unlimited/unavailable self-observation; root corrected last-observed-sample wording to match unchanged last_over_time. Full/delta guarded AST/JSON reviews pass; every non-description value/query/signal/inventory unchanged. No permanent prose snapshots added; scratch baseline validation fails and candidate passes. CodeRabbit skipped independently verified prose/declarative-only change, not clean review. Final candidate G and exact fresh composed G exit0. Land 414e401a5605ff63296ef4f3d21ae2d3c1a96d90; CI 38318318587 ci-success 115042574101 success; grafana-sync 38318318660 success and exact far-side health blob matches. helm-success absent. Pre-existing missing-limit/zero-overflow value-state issue filed TSO-0196 (distinguish absent cardinality telemetry from unlimited/zero overflow), excluded. Evidence /Users/rob/repos/tailscale2otel/codex/loop30/tso0183/.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed portable cardinality descriptions at 414e401a5605ff63296ef4f3d21ae2d3c1a96d90. Rendered prose matches current reporter/configured-limit semantics; query/runtime behavior unchanged. Final local/composed gates and exact CI/GitSync proofs complete.
<!-- SECTION:FINAL_SUMMARY:END -->
