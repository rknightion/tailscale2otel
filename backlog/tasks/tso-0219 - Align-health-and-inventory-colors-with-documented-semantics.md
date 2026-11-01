---
id: TSO-0219
title: Align health and inventory colors with documented semantics
status: To Do
assignee: []
created_date: '2026-11-01T22:20:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 221000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Capability availability history paints supported rows red. Uptime, Process uptime, ACL change evidence age, ACL size and Device key duration also receive warning-like red without a justified health verdict in their descriptions. This is distinct from missing-data colors. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Supported capability states are visually distinguishable from unavailable or failed states with consistent documented semantics.
- [ ] #2 Inventory or elapsed-time values use neutral presentation unless a stated threshold or objective justifies a health color.
- [ ] #3 Known healthy, failing and neutral fixtures exercise the intended mappings in retained renders.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
