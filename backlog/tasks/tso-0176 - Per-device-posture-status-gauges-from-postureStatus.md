---
id: TSO-0176
title: Per-device posture status gauges from postureStatus
status: Done
assignee:
  - '@loop30'
created_date: 2026-10-11 10:40
updated_date: '2026-11-01T21:40:00Z'
labels: []
dependencies:
  - TSO-0175
references:
  - https://tailscale.com/docs/features/device-posture/monitoring-postures
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
- [x] #1 A read-only capture of the lab devices list, with fields=all and with fields=id,postureStatus, is saved in the gitignored .capture/ and a synthetic fixture of the same shape drives the decode test
- [x] #2 A device failing an impacting posture emits passing 0 and impacting 1 for that posture and increments the tailnet rollup of failing devices per posture
- [x] #3 With per-entity device cardinality disabled only the rollups are emitted, and posture names are capped
- [x] #4 A failingAssertions log record fires once per change for a device, not on every poll
- [x] #5 A shipped alert rule for devices failing an impacting posture is generated through its gen recipe and the signal reaches a dashboard panel
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
First capture read-only devices lists fields=all and fields=id,postureStatus into ignored capture paths, inspect actual posture population/shape; D68 parks on zero postures and releases fleet successor. If observed, implement bounded posture gauges/rollups/change logs plus generated alert/panel and recorder fixtures; stage candidate, serialized G, guarded review/CodeRabbit, root composed/CI/delivery and rule-set readback.

Loop33 fresh populated capture prerequisite cleared; implement bounded posture gauges, change logs, rollups and generated surfaces with recorder proof.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop26: attempts 0; undispatched, dependency park. TSO-0175 (route-selection mode and truthful regional_routing) is parked behind the dashboard audit. No devices-list GET, capture, posture decode, generated alert edit, dashboard change or live rule push occurred; the lab posture population/wire shape remains UNKNOWN, not empty. Resume after the prerequisite is accepted and landed with a green composed gate, then make the required fields=all and fields=id,postureStatus read-only captures before any implementation. Preserve D53: empty/absent postureStatus with no configured postures parks this task, never guess the shape.

loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

loop30: 0 implementation attempts; authorized fields=all and fields=id,postureStatus GET captures succeeded but both returned only empty postureStatus objects, so no populated shape/fixture can be proven. D68 park, fleet successor released. Captures/receipt remain ignored in /Users/rob/repos/tailscale2otel/codex/loop30/worktrees/tso0176/.capture/tso0176/. No tracked changes, gate, rules publication or tailnet mutation. Resume on authorized populated real captures or a concrete owner-approved wire/proof source; configured posture definitions were not independently observed.

2026-11-01 loop33 prep (owner-authorised, non-impacting lab policy change): root cause of loop30's empty captures found. The devices API reports postureStatus as {} for every device until at least one grant uses srcPosture; defining postures alone evaluates none, and failingPostures returns nothing. A probe posture plus an additive grant to a device-less tag now switch evaluation on. Observed wire shape (structural only): postureStatus is an object keyed by posture name; each value is {passing: bool, impacting: bool, failingAssertions: [string]}, one entry per DEFINED posture (not only referenced ones). All three states are present in the lab: passing; failing and not impacting; failing and impacting. fields=all also carries postureStatus. Fresh captures for AC #1 are the loop's job under the Standing read-only grant.

loop33 landed03268855bff6fcad43f6c302e6c76a32d606410f. Fresh fields=all and id,postureStatus captures both populated all3states; synthetic same-shape decoder and recorder fixtures prove bounded gauges/rollups/change logs. PII filtered assertions, cap50 names, device toggle, recovery/removal/fetch failure, impacting alert firing and selector negative proof. Independent guarded PASS; stale-field dsreview major disproven by actual candidate. Exact composed gate20261101T213330Z-tailscale2otel-bc80e6ab exit0 with canonical outputs/dispositions/no-diff. Published source is distinct from current deployed exporter5.2.1.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added server-evaluated device posture verdict gauges, capped fleet rollups and change-only PII-filtered assertion logs, plus generated panel/alert and evidence-backed schema synthesis correction.
<!-- SECTION:FINAL_SUMMARY:END -->
