---
id: TSO-0092
title: Retire the 35-panel ceiling and re-group the whole dashboard tab structure
status: To Do
assignee: []
created_date: '2026-08-30 18:32'
labels:
  - needs-triage
milestone: m-7
dependencies: []
priority: high
ordinal: 93000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
OWNER DECISION 2026-08-30: there is no panel-count ceiling. The ~35-panel guideline in deploy/grafana/gen/dashboards.py:55 and :94 is retired. Panel count per tab and per tab group does not matter; what matters is that groupings are logical and operationally meaningful and that every panel earns its place. Rows are the tool for density — a tab may carry many rows, and a row may ship COLLAPSED by default when its detail is not needed on first open.

This supersedes the sizing pressure that degraded two Wave 2 deliverables: TSO-0060 folded two WAL panels into one capacity panel and TSO-0065 folded the eviction-age signal into an existing dedup diagnostics panel, both purely to stay under 35. Both consolidations are to be reconsidered on their merits now the constraint is gone; keep a merge only where the merged panel is genuinely the better panel.

Current per-module panel counts (grep of panel( calls, 2026-08-30): health_ingestion 35, health_collection 35, nodemetrics 32, k8saudit 32, tailnet_overview 31, devices_inventory 31, network 28, health_runtime 25, health_delivery 24, health_overview 21, devices_connectivity 21, security_identity 20, cardinality 20, policy_integrations 19, policy_identity 16, security_compliance 15, policy_dns 15, security_audit_trail 14, devices_posture 13, policy_access 10, security_risk 9.

The regroup is a whole-structure pass across both dashboards, not a per-tab tweak, so it is a single-owner job and every other lane that needs a panel returns the panel spec to it rather than editing tabs/ directly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The ~35-panel ceiling is removed from dashboards.py comments and from any doc or task text that cites it as a constraint
- [ ] #2 Both dashboards' tab and domain structure is re-grouped so each tab is a coherent operational question, with the rationale recorded on this task
- [ ] #3 Rows are used for density, with collapsed-by-default rows for detail that is not needed on first open, and the collapse decision stated per row
- [ ] #4 The two Wave 2 consolidations (TSO-0060 WAL panels, TSO-0065 eviction age) are re-examined and either split back out or kept with a stated reason
- [ ] #5 Every signal the wave adds lands on a real panel, and internal/catalog signal-coverage stays green with no empty dispositions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
