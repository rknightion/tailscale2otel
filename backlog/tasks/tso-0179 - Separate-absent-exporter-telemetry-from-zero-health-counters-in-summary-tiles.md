---
id: TSO-0179
title: Separate absent exporter telemetry from zero health counters in summary tiles
status: Parked
assignee: []
created_date: '2026-10-11 12:09'
updated_date: '2026-10-11 17:36'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: high
type: bug
ordinal: 179000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Both current-window Overview screenshots display green zeros despite absent exporter samples. Heartbeat/source absence and no observed errors are different outcomes. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 No heartbeat/scrape/exporter samples produces a missing-telemetry state in every named tile.
- [ ] #2 Present healthy source plus no event counter produces a prerequisite-aware zero where appropriate.
- [ ] #3 Verify zero-event and missing-source states via actual local query fixtures and a rendered summary row.
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

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.
<!-- SECTION:NOTES:END -->
