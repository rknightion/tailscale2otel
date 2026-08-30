---
id: TSO-0032
title: >-
  Per-runtime shutdown is sequential, contradicting the deliberate
  parallel-shutdown fix
status: To Do
assignee: []
created_date: '2026-08-30 08:45'
labels: []
dependencies: []
priority: low
type: bug
ordinal: 35000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Flow-store closes and rollup flushes run sequentially per tailnet runtime (internal/app/app.go:783-792 and 988-994) while telemetry shutdown was explicitly parallelized for the same shared-budget problem (#204, internal/telemetry/provider.go:447-478). One slow SQLite close can consume the shared shutdown budget before other tailnets flush, losing data in multi-tailnet deployments. No comment explains the exemption, so this is plausibly an oversight rather than a choice. Suspected during a product-surface review (2026-08-30), unproven - confirm the budget interaction first, then either parallelize with the same pattern as #204 or document why sequential is correct here.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Per-runtime close/flush either runs in parallel under the shared shutdown budget or carries a comment justifying sequential order
- [ ] #2 Multi-tailnet shutdown cannot lose one runtime flush because another runtime was slow, or the limitation is documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
