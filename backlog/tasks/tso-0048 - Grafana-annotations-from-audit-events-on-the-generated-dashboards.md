---
id: TSO-0048
title: Grafana annotations from audit events on the generated dashboards
status: To Do
assignee: []
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-2
dependencies: []
priority: low
ordinal: 51000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Audit logs already land in Loki; add annotation queries to the generated dashboards (deploy/grafana/gen) overlaying key config-change events - ACL changed, device added/deleted, key created - on the tailnet graphs. Pure dashboard-generator work: no exporter changes. Verify the annotation query shape works in the Grafana v2 schema the generator emits, and regenerate artifacts (gen-dashboards + counts + promqlcheck leg).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Generated dashboards carry Loki annotation queries for at least ACL change, device add/delete and key creation
- [ ] #2 Artifacts regenerated; drift and promqlcheck gates green
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
