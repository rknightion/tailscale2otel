---
id: TSO-0175
title: >-
  Emit the tailnet route-selection mode and stop regional_routing misreporting
  the newer modes
status: Done
assignee:
  - '@loop30'
created_date: '2026-10-11 10:40'
updated_date: '2026-10-12 21:15'
labels: []
dependencies:
  - TSO-0174
  - TSO-0191
references:
  - 'https://tailscale.com/docs/features/route-selection'
priority: medium
type: bug
ordinal: 175000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Tailnet settings now carry routeSelection (active-passive-failover, regional-routing, regional-routing-failover, magicroute) and regionalRoutingOn is read-only, nullable and deprecated in its favour. The settings collector emits regional_routing from that bool, so a tailnet on regional-routing-failover or MagicRoute probably reads 0, and a null decodes to a healthy-looking false. Operators cannot see which routing mode is active. Depends on the dashboard audit because a new signal needs a panel or disposition.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A settings fixture with routeSelection magicroute emits the mode as a bounded value, and an unrecognised future value folds to unknown
- [x] #2 regional_routing is derived from routeSelection when present, and a null regionalRoutingOn with no routeSelection emits no regional_routing point rather than false
- [x] #3 The new signal reaches a dashboard panel or a recorded disposition, and docs/metrics.md is regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement bounded route-selection signal and nullable legacy derivation with recorder fixtures; add panel/coverage and regenerate; stage owned candidate, run G, fresh security review then CodeRabbit; root lands and verifies composed gate/CI/delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop26: attempts 0; undispatched, dependency park. TSO-0172 (comprehensive live dashboard audit) is parked because dashboard-only delivery would trigger forbidden rule prune/push, with additional absence/control/render evidence incomplete. The frozen shared-file chain requires that task and TSO-0174 (current tagged-client migration) complete first. No settings/metrics/dashboard change was attempted. Resume by accepting and landing the dashboard audit under explicit authority, confirming the client migration Done, then dispatching this full task against their composed-green main SHA.

loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

2026-10-12 re-scope: now waits on TSO-0191 (selector-scope guard landing) instead of TSO-0172 (live dashboard audit); the audit runs after the chains.

loop30: 3 implementation attempts (worker, retry, first rescue), no security-repair round. Root amended missing generated counts/coverage/alert-link ownership; supplied five public-summary count substitutions; amended one bounded PII classification prerequisite. Real HTTP->collector->Recorder and real Provider/Prometheus transitions prove bounded mode, nullable regional derivation and removal of obsolete SDK points; failing regression captured before fixes. Snapshot body remains bounded. Final candidate G exit0, fresh guarded security review passed, CodeRabbit complete zero findings. Land 84ab7ca064a4f96e125cabbff1789631530109f0; fresh detached composed G exit0; CI 38308389717 ci-success 115010812761 success; grafana-sync 38308389690 success and exact far-side tailnet artifact blob matches. helm-success absent. Two-hour table evidence window can show historic modes, accepted as display limitation and tracked separately in TSO-0193 (distinguish prior route-mode observations from current state). No live tailnet mutation. Evidence /Users/rob/repos/tailscale2otel/codex/loop30/tso0175-rescue/.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed bounded route-selection mode, nullable legacy fallback and snapshot gauge correctness at 84ab7ca064a4f96e125cabbff1789631530109f0. Generated panel, count/coverage/docs and shifted alert links remain in sync. Local runtime/gates, guarded review, exact CI and GitSync delivery verified; no claim of erasing remote history.
<!-- SECTION:FINAL_SUMMARY:END -->
