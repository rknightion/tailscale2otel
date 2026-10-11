---
id: TSO-0172
title: Comprehensive audit of both Grafana dashboards against the live stack
status: To Do
assignee: []
created_date: '2026-10-11 10:33'
labels: []
dependencies: []
references:
  - deploy/grafana/gen
  - reference/grafana-delivery.md
priority: high
type: chore
ordinal: 172000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The last whole-dashboard live audit (TSO-0022) predates a long run of changes: the tab regroup (TSO-0092), new visualisation types (TSO-0150), HA coordination and ingress WAL panels, and many collector additions. TSO-0170 found the whole Node Metrics tab ignoring the tailnet and provider variables. Nothing caught it because the selector-scope test from TSO-0022 covered only the Policy and Config tabs, so similar gaps may exist on other tabs. This task audits every tab of the Tailnet and Exporter Health dashboards as rendered on the live stack. It produces a findings record and follow-up tasks. The fixes themselves belong in those follow-ups unless one is a single generated-query correction small enough to land here. Dashboards are delivered only through grafana-sync; never push them with gcx. Keep lab names, device names, addresses and raw telemetry values out of every tracked file, this task included.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every panel on every tab of both generated dashboards is inventoried with a verdict (correct, misleading, broken, redundant or missing-signal) and a one-line reason, recorded in the task notes without lab identifiers
- [ ] #2 Every metric, log and trace query is executed against the live stack over a stated window; each one either returns data or has its absence explained (feature disabled, prerequisite absent, or genuinely zero), and no panel shows a query error
- [ ] #3 Every query honours every dashboard variable that applies to it (tailnet, provider and any tab-local selector); a generated-dashboard test enforces this across all tabs, not only Policy and Config, and is seen failing on a deliberately unscoped query before it passes
- [ ] #4 Each signal the exporter emits (per internal/catalog) reaches a panel or has a documented disposition, and each panel or row whose question no operator would act on is flagged for removal or merge
- [ ] #5 Every finding not fixed in this task is filed as its own Backlog task with observable acceptance criteria, and this task lists the IDs it filed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
