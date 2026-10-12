---
id: TSO-0176
title: Per-device posture status gauges from postureStatus
status: In Progress
assignee:
  - '@loop30'
created_date: '2026-10-11 10:40'
updated_date: '2026-10-12 21:15'
labels: []
dependencies:
  - TSO-0175
references:
  - 'https://tailscale.com/docs/features/device-posture/monitoring-postures'
priority: high
type: feature
ordinal: 176000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Tailscale launched device posture monitoring and alerts (beta) on 2026-10-08. The device list now carries postureStatus with passing, impacting and failingAssertions, the server-side verdict that TSO-0039 approximates from raw attributes. The spec says it reports the status of a single posture, but the docs imply one entry per posture, and only show it requested as fields=id,postureStatus, so the wire shape and whether fields=all includes it are unconfirmed. Assertion text may embed attribute values: it must never become a label. Capture under the repo rules (gitignored .capture/, read-only GET, nothing from it in a tracked file). If the lab tailnet has no postures defined and the capture is empty, park with that evidence rather than guessing a shape.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A read-only capture of the lab devices list, with fields=all and with fields=id,postureStatus, is saved in the gitignored .capture/ and a synthetic fixture of the same shape drives the decode test
- [ ] #2 A device failing an impacting posture emits passing 0 and impacting 1 for that posture and increments the tailnet rollup of failing devices per posture
- [ ] #3 With per-entity device cardinality disabled only the rollups are emitted, and posture names are capped
- [ ] #4 A failingAssertions log record fires once per change for a device, not on every poll
- [ ] #5 A shipped alert rule for devices failing an impacting posture is generated through its gen recipe and the signal reaches a dashboard panel
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
First capture read-only devices lists fields=all and fields=id,postureStatus into ignored capture paths, inspect actual posture population/shape; D68 parks on zero postures and releases fleet successor. If observed, implement bounded posture gauges/rollups/change logs plus generated alert/panel and recorder fixtures; stage candidate, serialized G, guarded review/CodeRabbit, root composed/CI/delivery and rule-set readback.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop26: attempts 0; undispatched, dependency park. TSO-0175 (route-selection mode and truthful regional_routing) is parked behind the dashboard audit. No devices-list GET, capture, posture decode, generated alert edit, dashboard change or live rule push occurred; the lab posture population/wire shape remains UNKNOWN, not empty. Resume after the prerequisite is accepted and landed with a green composed gate, then make the required fields=all and fields=id,postureStatus read-only captures before any implementation. Preserve D53: empty/absent postureStatus with no configured postures parks this task, never guess the shape.

loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.
<!-- SECTION:NOTES:END -->
