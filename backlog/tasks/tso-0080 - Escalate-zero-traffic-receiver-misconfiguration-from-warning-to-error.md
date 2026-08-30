---
id: TSO-0080
title: Escalate zero-traffic receiver misconfiguration from warning to error
status: To Do
assignee: []
created_date: '2026-08-30 09:35'
labels: []
dependencies: []
priority: medium
ordinal: 83000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A non-loopback receiver bind with no token/secret starts and 403s everything - a warning today, while comparably broken configs (pprof without admin) hard-fail in Validate() (internal/config/validate.go). Non-functional deserves the hard-fail treatment insecure gets. Escalate to a validation error with a documented override if someone genuinely wants auth-off on loopback-adjacent networks.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The configuration fails validation with an actionable message
- [ ] #2 Intentional configurations retain an explicit, documented path
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
