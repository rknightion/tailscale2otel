---
id: TSO-0150
title: Diversify Grafana dashboard visualisations
status: To Do
assignee: []
created_date: '2026-09-19 09:52'
labels: []
dependencies: []
references:
  - 'https://grafana.m7kni.com/d/cloudflare-network-flow/cloudflare-network-flow'
  - >-
    https://grafana.com/docs/grafana-cloud/observe-and-act/monitor-infrastructure/integrations/integration-reference/integration-ktranslate-netflow/#dashboards
  - >-
    https://github.com/grafana/jsonnet-libs/blob/master/netflow-mixin/dashboards_out/netflow-overview.json
  - 'https://grafana.com/grafana/plugins/search/?type=datasource%2Cpanel'
priority: high
type: feature
ordinal: 151000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The generated dashboard family relies too heavily on conventional time series, stats, and tables, which hides relationship-heavy use cases such as network flow paths. The dashboard should use a broader but disciplined visual vocabulary, informed by the live m7kni Cloudflare flow dashboard, Grafana's NetFlow integration dashboard, and Grafana Cloud's installable panel catalogue, while remaining portable when optional plugins are absent.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both full-flow and rollup flow modes provide useful relationship visualisations and continue to respect their existing conditional rendering gates
- [ ] #2 Every dashboard tab is audited and any added non-default panel type has a documented analytical purpose rather than visual novelty
- [ ] #3 Optional plugin panels fail harmlessly when unavailable and identify the exact plugin to install
- [ ] #4 Generated Grafana v2 artifacts reproduce deterministically and all dashboard generator contract tests pass
- [ ] #5 The edited dashboards are visually verified on the m7kni stack after GitSync deployment
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
