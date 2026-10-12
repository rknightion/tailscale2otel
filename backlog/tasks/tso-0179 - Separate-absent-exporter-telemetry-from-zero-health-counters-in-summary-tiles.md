---
id: TSO-0179
title: Separate absent exporter telemetry from zero health counters in summary tiles
status: Done
assignee:
  - '@loop30'
created_date: '2026-10-11 12:09'
updated_date: '2026-10-12 21:06'
labels:
  - needs-triage
dependencies:
  - TSO-0191
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
- [x] #1 No heartbeat/scrape/exporter samples produces a missing-telemetry state in every named tile.
- [x] #2 Present healthy source plus no event counter produces a prerequisite-aware zero where appropriate.
- [x] #3 Verify zero-event and missing-source states via actual local query fixtures and a rendered summary row.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Identify summary tiles, preserve selected scope and distinguish source absence from no events; execute real local query fixtures and render summary row; regenerate and stage candidate, run serialized G, fresh security review then CodeRabbit; root lands and verifies composed gate/CI/delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

2026-10-12 re-scope: health chain head waits on TSO-0191 (selector-scope guard landing), which regenerates the same health dashboard JSON; no longer waits on TSO-0172 (live dashboard audit).

loop30: 1 implementation attempt, no review repair. Seven summary stat tiles have neutral missing-telemetry states; collector/API zeros require actual selected source observations. Actual promtool 45 cases and local Grafana13/Chromium row renders with seven exact query requests/state pass; initial zero-query screenshot rejected. Final full G exit0; fresh security reviewed every Python hunk. CodeRabbit ran once but No files to review exit1: two Python files unreviewed, not clean, covered separately by D69 security review. Land 2cd8d44e55622a2a6e35c857ccdd163870e05956; fresh detached composed G exit0; CI 38307363122 ci-success 115007732639 success; grafana-sync 38307363924 success and far-side health blob matches. helm-success absent. One 1-second setup timeout infrastructure retry, no attempt charged. Evidence /Users/rob/repos/tailscale2otel/codex/loop30/tso0179/.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed 2cd8d44e55622a2a6e35c857ccdd163870e05956: absent exporter telemetry no longer renders fabricated healthy summary zeros. Local query/render proofs and exact composed/CI/delivery checks pass. Historical lookback/threshold semantics retained; no claim of immediate outage detection or full-dashboard browser coverage.
<!-- SECTION:FINAL_SUMMARY:END -->
