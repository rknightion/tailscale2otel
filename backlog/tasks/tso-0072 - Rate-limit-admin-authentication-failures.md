---
id: TSO-0072
title: Rate-limit admin authentication failures
status: To Do
assignee: []
created_date: '2026-08-30 09:34'
labels: []
dependencies: []
priority: high
ordinal: 75000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
requireAdminAuth (internal/app/admin.go:226-256) counts rejections but never throttles; on a network-reachable bind the admin token is an unlimited online brute-force target. Add per-source backoff/limiting on the rejected path (cheap, memory-bounded), with self-obs for lockout events. Security-surface change: adversarial review tier.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Repeated failures from one source are throttled with bounded memory
- [ ] #2 Legitimate auth after backoff expiry works; behaviour tested
- [ ] #3 Lockout/throttle events observable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
