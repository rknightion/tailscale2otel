---
id: TSO-0055
title: Classify nodemetrics scrape failures beyond transient_failure
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-4
dependencies: []
priority: medium
ordinal: 58000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Every node-metrics scrape failure classifies uniformly as transient_failure (internal/collector/nodemetrics/nodemetrics.go:121-133). An operator debugging fleet-wide node.up=0 cannot tell "ACL blocks port 5252" from "node down" from "tailscaled too old, no metrics endpoint". Classify connection-refused / timeout / 404 / non-200 distinctly in the failure-reason attribute and surface a diagnostic hint on the admin status page.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Distinct failure classes are emitted for refused, timeout, missing-endpoint and other HTTP errors
- [ ] #2 Status page shows per-class counts or a hint for the dominant failure class
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
