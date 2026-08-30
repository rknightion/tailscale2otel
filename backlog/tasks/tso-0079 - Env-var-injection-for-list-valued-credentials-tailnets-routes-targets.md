---
id: TSO-0079
title: 'Env-var injection for list-valued credentials (tailnets, routes, targets)'
status: In Progress
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-30 23:22'
labels: []
milestone: m-6
dependencies: []
priority: medium
ordinal: 82000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
tailnets:, streaming.routes, webhook.routes and node_metrics.targets are file-only; an operator whose secret tooling only injects env vars cannot supply per-tailnet secrets. Add a name-keyed convention (e.g. TS2OTEL_TAILNET_<name>__CLIENT_SECRET) merging into matching list entries, covering at minimum per-tailnet OAuth secrets. Define precedence (env over file) consistently with the existing layering.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A per-tailnet client secret can be supplied via env with no secret in YAML
- [ ] #2 Merging/precedence rules documented and tested, including the no-matching-entry case
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
F1 records that no persistent config key is needed: this task adds an environment overlay convention for existing list-valued credential fields; lane J later implements merge and precedence tests.
<!-- SECTION:PLAN:END -->
