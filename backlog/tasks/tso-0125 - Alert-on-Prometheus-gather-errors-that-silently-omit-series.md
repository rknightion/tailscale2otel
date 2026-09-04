---
id: TSO-0125
title: Alert on Prometheus gather errors that silently omit series
status: Done
assignee: []
created_date: '2026-09-04 06:35'
updated_date: '2026-09-04 08:41'
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
- [x] #1 A generated advisory non-paging rule detects a non-zero 10-minute gather-error rate for 15 minutes
- [x] #2 The alert is scoped to the optional pull endpoint and executable fixtures cover sustained error and healthy cases
- [x] #3 Grafana-managed and Prometheus artifacts plus runbook documentation regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Done in 7841543f. ts2o-pull-gather-errors: sum(rate(tailscale2otel_metrics_scrape_gather_errors_total[10m])) > 0 for 15m, advisory, non-paging, policy optional (the pull endpoint is opt-in and most deployments export over OTLP only). Runbook exporter-internal-errors, panel 'Pull-endpoint gather errors/s'. Fixtures deliberately use a flat counter as the healthy case rather than an absent series: the handler returns HTTP 200 either way, so no errors and endpoint disabled look identical from outside and only the flat series distinguishes them. Negative-tested individually. Live-verified in the same push as TSO-0124.
<!-- SECTION:NOTES:END -->
