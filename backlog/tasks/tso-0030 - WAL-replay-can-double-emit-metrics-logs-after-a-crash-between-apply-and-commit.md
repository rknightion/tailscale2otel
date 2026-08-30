---
id: TSO-0030
title: WAL replay can double-emit metrics/logs after a crash between apply and commit
status: To Do
assignee: []
created_date: '2026-08-30 08:45'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-1
dependencies: []
priority: medium
type: bug
ordinal: 33000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/app/ingresswal.go:305-363 tracks envelope phase (pending/applied/flushed) in an in-memory map only. A crash after route.apply has emitted telemetry but before the WAL marks the envelope done means replay re-emits the same counters/logs; the processor-level dedup sets are also in-memory so they cannot catch it. The config comment documents export duplication but not the metric double-count. Suspected during a product-surface review (2026-08-30), unproven - first establish whether this window is real by reading the replay path end-to-end (or reproducing with a crash test), then decide: persist a per-envelope applied marker, or document the at-least-once metric duplication contract as loudly as the export one.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The apply-to-commit crash window is either closed (persisted applied marker) or explicitly documented as an accepted at-least-once duplication of metrics as well as exports
- [ ] #2 A test or written analysis demonstrates the chosen behaviour under crash-during-apply
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
