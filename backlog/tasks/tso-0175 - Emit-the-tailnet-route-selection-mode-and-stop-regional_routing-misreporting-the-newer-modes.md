---
id: TSO-0175
title: >-
  Emit the tailnet route-selection mode and stop regional_routing misreporting
  the newer modes
status: Parked
assignee: []
created_date: '2026-10-11 10:40'
updated_date: '2026-10-11 12:31'
labels: []
dependencies:
  - TSO-0174
  - TSO-0172
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
- [ ] #1 A settings fixture with routeSelection magicroute emits the mode as a bounded value, and an unrecognised future value folds to unknown
- [ ] #2 regional_routing is derived from routeSelection when present, and a null regionalRoutingOn with no routeSelection emits no regional_routing point rather than false
- [ ] #3 The new signal reaches a dashboard panel or a recorded disposition, and docs/metrics.md is regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop26: attempts 0; undispatched, dependency park. TSO-0172 (comprehensive live dashboard audit) is parked because dashboard-only delivery would trigger forbidden rule prune/push, with additional absence/control/render evidence incomplete. The frozen shared-file chain requires that task and TSO-0174 (current tagged-client migration) complete first. No settings/metrics/dashboard change was attempted. Resume by accepting and landing the dashboard audit under explicit authority, confirming the client migration Done, then dispatching this full task against their composed-green main SHA.
<!-- SECTION:NOTES:END -->
