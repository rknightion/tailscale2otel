---
id: TSO-0176
title: Per-device posture status gauges from postureStatus
status: To Do
assignee: []
created_date: '2026-10-11 10:40'
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
