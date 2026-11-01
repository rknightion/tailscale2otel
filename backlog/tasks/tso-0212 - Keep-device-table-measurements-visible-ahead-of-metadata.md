---
id: TSO-0212
title: Keep device table measurements visible ahead of metadata
status: To Do
assignee: []
created_date: '2026-11-01T22:06:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_inventory.py
priority: high
type: bug
ordinal: 214000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
At the shipped Inventory and Hygiene table widths, Device key expiry (time until), Last seen (time since), and Updates available show metadata columns while their question-answering duration or version measurements are outside the visible table area. Filed by TSO-0172 (comprehensive live dashboard audit); no lab identifiers or raw values are included. Outside the approved loop33 fix set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each affected table shows a device identifier and its named duration or update/version measurement together at the default shipped width.
- [ ] #2 Extra resource metadata does not push the named measurement offscreen; necessary metadata remains accessible.
- [ ] #3 Synthetic frames with additional metadata columns demonstrate the prior failure and corrected default presentation.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
