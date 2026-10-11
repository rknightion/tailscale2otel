---
id: TSO-0187
title: Make automatic rule publication explicit in dashboard-only loop authority
status: To Do
assignee: []
created_date: '2026-10-11 12:09'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: task
ordinal: 187000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The existing grafana-sync workflow deliberately prunes and pushes rules on every dashboard-path push. A campaign that authorizes GitSync dashboard delivery while forbidding these side effects cannot safely publish. Do not silently change this established delivery behavior; the owner must choose an explicit automation grant or a separately approved workflow change. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Repository loop preparation documentation explicitly enumerates the automatic rule prune/push on dashboard-only delivery.
- [ ] #2 A dashboard-write campaign records an owner-approved choice covering the existing side effects or separately authorizing a delivery-behavior change, before publication.
- [ ] #3 Any separately approved workflow change preserves the existing authorized rule-delivery path and is covered by failing-before/passing-after workflow-contract proof.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
