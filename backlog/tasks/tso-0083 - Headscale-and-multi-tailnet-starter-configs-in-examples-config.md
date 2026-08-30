---
id: TSO-0083
title: Headscale and multi-tailnet starter configs in examples/config
status: To Do
assignee: []
created_date: '2026-08-30 09:36'
updated_date: '2026-08-30 09:48'
labels: []
milestone: m-7
dependencies:
  - TSO-0077
priority: medium
ordinal: 86000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
examples/config/ has only grafana-cloud, prometheus and stdout starters despite Headscale support and multi-tailnet/MSP mode being headline README features - real onboarding friction for two first-class paths. Add a Headscale starter and a multi-tailnet starter, validated by configcheck like the existing examples, and link them from the README/docs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both starters exist, pass configcheck, and are referenced from the docs
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
