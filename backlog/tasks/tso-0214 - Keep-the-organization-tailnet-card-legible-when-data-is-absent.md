---
id: TSO-0214
title: Keep the organization-tailnet card legible when data is absent
status: To Do
assignee: []
created_date: '2026-11-01T22:06:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_inventory.py
priority: low
type: bug
ordinal: 216000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Organization tailnets stat card truncates its title and shrinks a long explanatory value to an unreadable size at the shipped card width. The observed issue is presentation, not proof that organization discovery should be enabled. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 fix set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The title and primary state are legible at the shipped card dimensions for both populated and absent data.
- [ ] #2 A full absence or prerequisite explanation is accessible without shrinking the primary state into unreadable text.
- [ ] #3 Retained renders cover both a populated synthetic response and a legitimate absent/prerequisite-disabled response.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
