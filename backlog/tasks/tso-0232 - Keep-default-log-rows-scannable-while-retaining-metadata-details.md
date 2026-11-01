---
id: TSO-0232
title: Keep default log rows scannable while retaining metadata details
status: To Do
assignee: []
created_date: '2026-11-01T22:55:00Z'
labels:
  - follow-up
  - dashboards
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: low
type: bug
ordinal: 234000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Flow log stream, Recent configuration changes and Log stream wrap inline metadata across many lines per entry, reducing scanability and visible event coverage at normal panel size. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Time, event classification and message remain scannable as coherent records at shipped widths.
- [ ] #2 Additional metadata remains accessible without making every record a large default block.
- [ ] #3 Representative short and metadata-heavy synthetic records render readably without losing detail access.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
