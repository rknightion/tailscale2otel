---
id: TSO-0224
title: Make ingestion and export cost comparisons dimensionally consistent
status: To Do
assignee: []
created_date: '2026-11-01T22:20:00Z'
updated_date: '2026-11-01T22:34:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 226000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Ingest vs export cost (per minute) mixes per-minute export volumes with per-second ingestion rates on one unlabeled numeric axis, and replica series share an ambiguous DPM legend. Forwarded metric-name drops/s by reason intentionally includes an active-series companion, but source promises a right count axis while only shared rate units/axis are configured. Seeing the companion legend alone is not evidence that the drop-rate series was mislabeled. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each plotted quantity has a descriptive legend that distinguishes quantity and relevant replica or source grouping.
- [ ] #2 Compared quantities share a time/unit basis or use clearly identified separate axes.
- [ ] #3 The forwarded-name-drop panel gives its intentional active-series companion the separate count axis described by the source, while the drop rate retains rate units and reason labels.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root source inspection qualified the initial visual interpretation: companion series B is intentional and has the correct active-series legend; its appearance without quiet series A is not a naming defect. The missing dimensional separation remains source-supported.
<!-- SECTION:NOTES:END -->
