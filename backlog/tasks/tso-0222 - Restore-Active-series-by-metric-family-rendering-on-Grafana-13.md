---
id: TSO-0222
title: Restore Active series by metric family rendering on Grafana 13
status: To Do
assignee: []
created_date: '2026-11-01T22:20:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: high
type: bug
ordinal: 224000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The retained Cost and Cardinality render shows An unexpected error happened in Active series by metric family instead of the visualization. Backend query success is not frontend plugin acceptance; the cause is not yet established. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Reproduce the actual frontend failure using a sanitized frame and supported Grafana/plugin version identity.
- [ ] #2 The shipped panel renders populated and empty data without an unexpected-error boundary on the supported Grafana version.
- [ ] #3 Retain browser/render evidence and any required plugin/version prerequisites without silently replacing the requested metric-family question.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
