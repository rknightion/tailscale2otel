---
id: TSO-0213
title: Make absent device security flags distinguishable and readable
status: To Do
assignee: []
created_date: '2026-11-01T22:06:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_inventory.py
priority: medium
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
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
