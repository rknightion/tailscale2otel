---
id: TSO-0198
title: Keep connectivity ratio populations consistent under OS filtering
status: To Do
assignee: []
created_date: '2026-10-13 22:59'
labels: []
dependencies: []
references:
  - >-
    /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_connectivity.py
priority: medium
type: bug
ordinal: 200000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Direct-capable % and Hard-NAT % dashboard expressions use fleet-wide aggregate numerators while their device-count denominator receives the OS selector. Selecting one OS can therefore compare different populations. Found while qualifying synthetic browser fixtures; this is a query semantic gap, not evidence of a production data leak.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A non-All OS selection compares matching numerator and denominator populations, or the panels explicitly remain fleet-scoped with consistent queries.
- [ ] #2 A mixed-OS fixture demonstrates the prior mismatch and verifies the corrected behavior without fabricating OS labels on fleet-wide aggregates.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
