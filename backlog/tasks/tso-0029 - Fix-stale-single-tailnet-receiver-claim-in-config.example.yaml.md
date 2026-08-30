---
id: TSO-0029
title: Fix stale single-tailnet receiver claim in config.example.yaml
status: To Do
assignee: []
created_date: '2026-08-30 08:45'
labels: []
dependencies: []
priority: low
type: bug
ordinal: 32000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
config.example.yaml:98-99 states streaming/webhook receivers require single-tailnet mode, but streaming.routes and webhook.routes (config.example.yaml:535-537, 553-555) provide explicit multi-tailnet receiver routing, and docs/configuration.md:433 documents routes as the multi-tailnet path. Likely a leftover comment from before routes landed. Suspected during a product-surface review (2026-08-30), not yet proven — verify what the loader/validator actually enforces before editing, then fix whichever side is wrong. Note docs/env-vars.md is generated from config.example.yaml comments, so regenerate after the edit (just gen-envref).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The example config, docs/configuration.md and actual validation behaviour agree on multi-tailnet receiver support
- [ ] #2 Generated docs regenerated if the example config comments change
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
