---
id: TSO-0213
title: Distinguish absent telemetry from healthy states in generated panels
status: To Do
assignee: []
created_date: '2026-11-01T22:06:00Z'
updated_date: '2026-11-01T23:26:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_inventory.py
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/health_ingestion.py
priority: high
type: bug
ordinal: 215000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Device security flags (per device) renders a long fallback sentence clipped across flag columns. The operator cannot reliably distinguish the flag state or read the absence explanation. Observed in a retained live render during TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 fix set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Known positive, known negative and absent flag states are visually distinct without clipping at shipped dimensions.
- [ ] #2 The absence explanation is fully available without pretending a missing value is a healthy zero.
- [ ] #3 A synthetic mixed-state fixture and retained panel render prove the corrected behavior.
- [ ] #4 Absent optional data uses a readable neutral or explicitly unknown state across the listed cards; populated healthy and failing states retain their intended distinction.
- [ ] #5 Present all-zero device-flag bases display numeric zero while absent bases remain unavailable, preserving OS/host/tailnet/provider scope.
- [ ] #6 Disabled or absent optional object-store feeds do not acquire a healthy zero from an unanchored fallback; observed feeds with zero exceptional events may show zero.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Additional observed green unknown/absent states: Posture names dropped, Multiple simultaneous connections, Blocking incoming connections, Stale users (>30d), Pending exit-node shares, Unrecognized desired events, Requests delayed by the rate limiter and Metrics over cardinality cap. Object-store gaps clear and Object listing complete show the same absence explanation on contradictory red/green backgrounds. Long fallback text is also unreadable in some cards. Keep optional absence separate from health verdicts.

Source review also proves two logical state errors: Multiple simultaneous connections and Blocking incoming connections use count(base == 1), which is empty when observed bases are all zero; Undecodable objects (broken feed) uses an unanchored zero fallback although no object-store feed is selected. Preserve known-zero, positive and absent populations separately, rather than solving only colors.

Additional exact bindings: Pull-endpoint gather errors/s invents a fallback zero beside absent pull-endpoint telemetry; Requests delayed by the rate limiter, Posture names dropped and Metrics over cardinality cap use green for missing evidence. Object-store gaps clear/listing complete and Audit poll collector use health/fault backgrounds for absent optional feeds. Checkpoint disk and Object-store age compress prerequisite text until unreadable. Preserve independent source/config absence explanations and avoid inferring health from missing samples.

Exhaustive binding also observes Active sessions displaying No data in green. Preserve the empty-session source explanation while giving absence a neutral presentation.
<!-- SECTION:NOTES:END -->
