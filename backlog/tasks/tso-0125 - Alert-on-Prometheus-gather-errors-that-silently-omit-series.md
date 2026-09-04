---
id: TSO-0125
title: Alert on Prometheus gather errors that silently omit series
status: To Do
assignee: []
created_date: '2026-09-04 06:35'
labels:
  - needs-triage
dependencies: []
priority: medium
type: feature
ordinal: 126000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The health dashboard visualizes tailscale2otel.metrics.scrape.gather_errors, but no rule watches it. A scrape may still return HTTP 200 while the Prometheus gatherer omits conflicting series, so operators otherwise receive no active signal for duplicate-label or filtering collisions.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A generated advisory non-paging rule detects a non-zero 10-minute gather-error rate for 15 minutes
- [ ] #2 The alert is scoped to the optional pull endpoint and executable fixtures cover sustained error and healthy cases
- [ ] #3 Grafana-managed and Prometheus artifacts plus runbook documentation regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
