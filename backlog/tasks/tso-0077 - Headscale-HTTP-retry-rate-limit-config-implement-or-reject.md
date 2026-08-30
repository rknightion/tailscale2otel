---
id: TSO-0077
title: 'Headscale HTTP retry/rate-limit config: implement or reject'
status: To Do
assignee: []
created_date: '2026-08-30 09:35'
labels: []
dependencies: []
priority: high
ordinal: 80000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
headscale.http.retry and rate_limit are accepted "for parity" but explicitly NOT applied (config.example.yaml:41-45) - accepted-but-inert config is the worst state. Either implement retry/rate-limit in internal/hsapi (mirroring the tsapi transport behaviour) or make setting them a validation error. Owner preference not yet stated between the two - decide on pickup with a bias to implementing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The keys are either honoured by hsapi (with tests) or rejected loudly at validation
- [ ] #2 config.example.yaml comment and generated docs updated to match reality
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
