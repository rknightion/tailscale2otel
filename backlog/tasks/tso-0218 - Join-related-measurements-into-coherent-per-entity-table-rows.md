---
id: TSO-0218
title: Join related measurements into coherent per-entity table rows
status: To Do
assignee: []
created_date: '2026-11-01T22:20:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 220000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Integration sync detail, Per-user detail and Connector health fragment the same entity into repeated sparse rows, leaving sibling measurements blank and making comparison difficult. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each entity has one coherent row for its related measurements at a stated evaluation time.
- [ ] #2 Missing measurements remain explicitly unknown and are not silently filled as healthy zero.
- [ ] #3 Synthetic multi-entity/multi-measure frames prove correct joins without cross-tenant or cross-entity mixing.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
