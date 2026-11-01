---
id: TSO-0220
title: Keep default annotations and time axes from obscuring traces
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
ordinal: 222000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Dense blue annotation regions dominate many Overview, Collection, Delivery, identity and network plots; rightmost time labels also clip at several panel edges. The underlying signal becomes hard to read despite successful queries. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Representative dense-event fixtures leave the primary time-series signals readable with default annotation settings.
- [ ] #2 Annotation filtering or presentation preserves access to the event context without overwhelming unrelated signals.
- [ ] #3 Normal-width renders retain readable edge time labels and meaningful legends.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
