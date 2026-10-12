---
id: TSO-0178
title: Anchor fleet summary zeros to observed source presence
status: Done
assignee:
  - '@loop30'
created_date: '2026-10-11 12:09'
updated_date: '2026-10-12 21:56'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: high
type: bug
ordinal: 178000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Current-window Tailnet Overview snapshot shows green zero while the exporter has no recent samples; these expressions use unanchored or vector(0) fallbacks. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 With no fleet/user/key source samples, each named tile explicitly reports missing source instead of healthy zero.
- [x] #2 With a present source and genuinely empty selected population, each named tile reports zero.
- [x] #3 Exercise both states in query fixtures and browser/render evidence without changing the normal populated result.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Freeze fleet/user/key summary tile inventory, anchor zero to true observed prerequisites; prove absent vs present-empty vs populated selected scope with actual PromQL and local Grafana row render; regenerate/stage, serialized G, guarded review/CodeRabbit, root composed/CI/delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

loop30: 1 implementation attempt, no review repair. Five exact Overview inventory/key tiles use actual successful scoped API-listing or per-device source evidence, never heartbeat, to distinguish missing from genuinely empty selected population. Actual PromQL engine covers nine states and populated preservation; seven real local Grafana/Chromium states each requested five exact expressions, screenshots reviewed. Full candidate G exit0, fresh guarded security all Python/artifact proofs passed. CodeRabbit ran once No files to review exit1, two Python files unreviewed/not clean under D69 separate review coverage. Land 373608d6e03bf960a5463c0851f29dc8125c4d50; exact composed G exit0; CI 38312495404 ci-success 115024282581 success; grafana-sync 38312495399 success and exact far-side tailnet blob matches. helm-success absent. Existing historical windows preserved, no latest-state transition claim. Portable listing-filter durability info filed TSO-0195 (harden listing prerequisite regression guard). Gate runner performed one unrequested already-proven base gate, recorded as excess verification, not necessary acceptance proof. Evidence /Users/rob/repos/tailscale2otel/codex/loop30/tso0178/.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed source-aware fleet summary at 373608d6e03bf960a5463c0851f29dc8125c4d50. Missing source, observed empty and normal populated results proven by actual query/render fixtures; exact gates/CI/GitSync verified. Historical lookback behavior retained.
<!-- SECTION:FINAL_SUMMARY:END -->
