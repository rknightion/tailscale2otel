---
id: TSO-0178
title: Anchor fleet summary zeros to observed source presence
status: Parked
assignee: []
created_date: '2026-10-11 12:09'
updated_date: '2026-10-11 15:29'
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
- [ ] #1 With no fleet/user/key source samples, each named tile explicitly reports missing source instead of healthy zero.
- [ ] #2 With a present source and genuinely empty selected population, each named tile reports zero.
- [ ] #3 Exercise both states in query fixtures and browser/render evidence without changing the normal populated result.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.
<!-- SECTION:NOTES:END -->
