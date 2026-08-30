---
id: TSO-0030
title: WAL replay can double-emit metrics/logs after a crash between apply and commit
status: Done
assignee: []
created_date: '2026-08-30 08:45'
updated_date: '2026-08-30 12:58'
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
- [x] #1 The apply-to-commit crash window is either closed (persisted applied marker) or explicitly documented as an accepted at-least-once duplication of metrics as well as exports
- [x] #2 A test or written analysis demonstrates the chosen behaviour under crash-during-apply
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. TDD, and the failing test IS the deliverable for AC#2. Add `TestIngressWALCoordinator_ReappliesAfterCrashBetweenApplyAndCommit` to internal/app/ingresswal_test.go:
   - build a real `ingresswal.Store` over `t.TempDir()`;
   - append one envelope; run a replay whose route `apply` succeeds but whose `flush` blocks or errors so `commitGeneration` never runs; assert applies == 1;
   - DROP that coordinator entirely and construct a SECOND `newIngressWALCoordinator` over the SAME store/directory (this is the crash — a fresh in-memory `progress` map);
   - replay again with a healthy route; assert `apply` was called a SECOND time for the same envelope ID.
   Watch it fail for the right reason first if the behaviour is ever "fixed", then keep it as the executable statement of the contract. Assert telemetry through `internal/telemetrytest.Recorder` (repo rule: assert emitted signals, not internals) so the doubled COUNTER is what the test pins, not just the call count.
2. Correct the config comment at config.example.yaml:511-514 so it names metrics and logs, not only exports. Suggested: "Replay is at-least-once: a crash between applying an envelope and committing it re-applies the whole envelope on restart, so exported data AND the metrics/log records derived from it can be counted twice. The in-memory cross-source dedup sets are rebuilt empty at startup and do not suppress this." Then `just gen-envref`.
3. Mirror the same statement in the operator-facing docs — docs/configuration.mds ingress_wal section and any receiver/durability page that currently repeats the export-only wording. Grep for "at-least-once" and fix every copy in the same commit.
4. Add a package-doc paragraph on `applyEnvelope` (internal/app/ingresswal.go) stating that `c.progress` is deliberately in-memory, that it covers in-process retries only, and that a persisted marker was considered and rejected because apply is not atomic with any marker write. Point at the new test by name so the next reader finds the evidence rather than re-deriving it.
5. Gate: `just check`.

Explicitly OUT of scope: persisting the applied phase, changing the WAL record format, and any exactly-once claim.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9) — CONFIRMED: the window is real and cannot be closed

Traced the replay path end to end.

`internal/ingresswal/wal.go:691 Store.Replay` iterates durable pending entries and, per entry, calls `handler(ctx, record.Envelope)` FIRST and `s.commitGeneration(ctx, id, seq)` only after the handler returns nil (lines 762-770). The on-disk completion marker is therefore written strictly AFTER the apply. A crash between those two points leaves the entry `pending` and `durable` on disk, so the next process replays it.

`internal/app/ingresswal.go:305 applyEnvelope` is the handler. Its phase ledger is `c.progress`, a plain `map[string]ingressWALProgress` on the coordinator (ingresswal.go:75), created fresh in `newIngressWALCoordinator` (line 109). Nothing about it is persisted. On a new process `progress[envelope.ID]` is the zero value, i.e. `ingressWALPhasePending`, so `route.apply` runs again and re-emits.

`route.apply` is the emitting call: `streamServer.ApplyDurable` / `webhookServer.ApplyDurable` (internal/app/collectors.go:498 and 515), which drive the real flow/audit processors and therefore the real counters and log records.

The dedup backstop does not help across a crash. `internal/dedup` is an in-memory bounded FIFO (`internal/dedup/dedup.go`), and both cross-source sets are rebuilt empty at startup (`internal/app/tailnetruntime.go:156,192`).

Window size: between `route.apply` returning and `commitGeneration` completing sits `route.flush` under `ingressWALFlushTimeout = 10s` (internal/app/ingresswal.go:21). So the exposure is up to ~10 seconds per envelope, not microseconds.

## Why a persisted applied marker does NOT close it — REJECT that option

The task offered "persist a per-envelope applied marker" as one of two outcomes. It is not a fix, for two independent reasons:

1. `route.apply` is not atomic with any marker write. A crash between the LAST emit inside apply and the marker fsync re-emits exactly as before. The window shrinks; it never closes.
2. `route.apply` is not atomic with itself. A crash PART WAY THROUGH apply (half an envelopes records emitted) replays the whole envelope regardless of any marker, because the marker is per envelope and the partial progress inside one is not recorded at all.

There is no atomic commit spanning "emitted to a remote backend" and "wrote a local marker", so at-least-once is inherent, not an implementation shortcut. Persisting a marker would add an fsync per envelope on the replay hot path and buy a smaller, still-open window. Recommend NOT doing it.

## Therefore: document, loudly, and prove it with a test

The existing wording already concedes half of it. `config.example.yaml:511` header: "Replay is at-least-once, so a crash can duplicate exported data." That is true of exports and silent about the metric/log double-count, which is the part an operator reading a counter would be surprised by.

Existing coverage in `internal/app/ingresswal_test.go` proves the IN-PROCESS retry paths do not re-apply (`TestIngressWALCoordinator_FlushRetryDoesNotReapply:357`, `..._CommitRetryDoesNotReapplyOrReflush:441`, `..._ProgressClearsAfterSuccessfulReplay:512`). Nothing covers a NEW coordinator over the same WAL directory — the crash case — and the absence of that test is why the in-memory ledger reads as durable.

Wave 1 Lane B2 started by root at 268fc93 after Lane 0; frozen decision is to document inherent at-least-once replay and not persist an applied marker.

TDD evidence: the crash-window replay test first failed with webhook log records = 2, want 1, proving the old exactly-once assumption was wrong. Final race tests and just check passed; exact-head CI run 33312668201 concluded success.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed d1d0a2b: document and prove WAL replay is at-least-once for derived metrics and logs after a crash between apply and commit. Verified by reopen/replay test, full gate, and CI 33312668201.
<!-- SECTION:FINAL_SUMMARY:END -->
