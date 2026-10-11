---
id: TSO-0182
title: >-
  Count distinct metric families rather than reporter series in the family-count
  tile
status: Parked
assignee: []
created_date: '2026-10-11 12:09'
updated_date: '2026-10-11 15:29'
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
- [ ] #1 A fixture with two reporters for one metric_name yields one distinct family.
- [ ] #2 Adding a second distinct metric_name increases the count to two.
- [ ] #3 Respect selected tailnet scope and document whether the tile is global or per selected runtime; preserve series-cost sum as a separate question.
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
