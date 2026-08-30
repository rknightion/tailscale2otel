---
id: TSO-0078
title: Document restart-required vs hot-reloadable config keys
status: To Do
assignee: []
created_date: '2026-08-30 09:35'
labels: []
dependencies: []
priority: medium
ordinal: 81000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Substantial engineering avoids restarts (credential/TLS reload, checkpoint durability) yet no doc states which keys hot-reload. Generate a table in docs/configuration.md from struct tags (or an equivalent single source of truth) marking each key restart-required vs hot-reloadable, gated against drift like the other generated docs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every config key carries a reload classification in generated docs
- [ ] #2 A drift gate fails when a new key lacks a classification
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
