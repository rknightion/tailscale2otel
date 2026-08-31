---
id: TSO-0059
title: Per-tailnet admission fairness on shared receivers
status: Done
assignee: []
created_date: '2026-08-30 09:31'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-4
dependencies: []
priority: medium
ordinal: 62000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Both receiver routers share one semaphore sized from the base MaxConcurrentRequests (internal/stream/stream.go:697-720, internal/webhook/webhook.go:403-422); one tailnet flooding the listener starves every other route. Add per-route admission sub-budgets (share of the global budget, or per-route override) so a noisy tenant cannot silence the rest in multi-tailnet mode. Consider interaction with the WAL admission path.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A flood on one route cannot consume the entire admission budget of the listener
- [x] #2 Budgets are configurable with sane defaults; behaviour covered by a concurrency test
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes per-route receiver admission budgets with defaults that preserve current global-only behaviour; lane E later implements fairness and concurrency tests.

Lane E consumes the frozen per-route admission limits to provide per-tailnet fairness on shared receivers, with focused concurrency tests.
<!-- SECTION:PLAN:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added configurable per-route admission budgets so a flood on one receiver route cannot consume the listener, with concurrency tests for streaming and webhook paths. Implementation SHA f35b6ab. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
