---
id: TSO-0032
title: >-
  Per-runtime shutdown is sequential, contradicting the deliberate
  parallel-shutdown fix
status: Done
assignee: []
created_date: '2026-08-30 08:45'
updated_date: '2026-08-30 12:58'
labels: []
milestone: m-1
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
- [x] #1 Per-runtime close/flush either runs in parallel under the shared shutdown budget or carries a comment justifying sequential order
- [x] #2 Multi-tailnet shutdown cannot lose one runtime flush because another runtime was slow, or the limitation is documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Correct the premise in one line where the next reader will hit it — a comment at internal/app/app.go:783 saying the flowStore closes run after the last budgeted stage and are NOT covered by `worstCaseDrain()`. The wrong belief is seductive; leave the correction next to the code.
2. Add a fifth budgeted stage. In internal/app/shutdownbudget.go add `flowStoreCloseTimeout = 10 * time.Second` with a comment naming what it bounds (`sqlitestore.Store.Close` -> `wg.Wait()` on the retention worker, then `db.Close()`), and extend the header comments stage list to five.
3. Run the closes concurrently under that one budget, using the SAME pattern and the SAME justification as #204: `internal/telemetry/provider.go:461 shutdownAll` already does exactly this (per-function child context, one shared deadline, joined errors, no goroutine leak). Either export/lift it to a shared helper or mirror it in `internal/app`; do not invent a third shutdown-fan-out shape. `sqlitestore.Store.Close()` takes no context, so the wrapper must run each close in its own goroutine and return on the budget deadline, logging the runtimes that did not finish — a blocked `wg.Wait()` cannot be cancelled, so the goroutine may outlive the wait and that must be stated in the comment rather than pretended away.
4. Add `flowStoreCloseTimeout` to `worstCaseDrain()` in internal/app/shutdownbudget_test.go. This RAISES `requiredBudget()`, which will fail `TestComposeStopGracePeriodCoversDrain` and the charts `terminationGracePeriodSeconds` assertion until `deploy/docker-compose.yaml` and `deploy/helm/tailscale2otel/values.yaml` are raised to match — that failure is the point of the test and is the acceptance evidence. Regenerate the chart artifacts (`just gen-helm`) after raising the value; the test message names it.
5. Leave the `FlushRollup` loop sequential and add a one-line comment justifying it (in-memory, no context, no I/O) — that satisfies AC#1 for that loop without a code change.
6. Tests, test-first: a multi-runtime shutdown where one runtimes flow store blocks past the budget still closes the others (assert the others Close ran), and the budget-sum test fails when the new stage is omitted (negative-test it — doc-0002 recurring defect).
7. Gate: `just check`. Note `internal/app` is the composition root and a single-owner wiring file (doc-0002 §3) — this lane must not run concurrently with another lane editing internal/app.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9) — stated mechanism REFUTED, a different real defect CONFIRMED

**The premise as written is WRONG.** The task says "one slow SQLite close can consume the shared shutdown budget before other tailnets flush". There is no shared budget over either loop, because neither loop takes a context.

`internal/app/shutdownbudget.go` enumerates the staged drain in its own header comment, and there are exactly four stages: schedulers stop (unbudgeted, returns promptly on the cancelled context), `receiverDrainTimeout` (10s, both receivers in parallel = one budget), `ingressWALFlushTimeout`, `telemetryFlushTimeout` (10s). `shutdownbudget_test.go:38 worstCaseDrain()` sums precisely those three timeouts.

Against that:

- **`rt.flowStore.Close()`** (internal/app/app.go:783-792) is a `defer` registered near the top of `Run`, so it executes at function exit — AFTER `a.shutdown(shutdownCtx)` at app.go:996-998 has already returned. It takes no context and no deadline. It cannot consume the telemetry budget because the telemetry budget is already spent by the time it runs.
- **`rt.flowProc.FlushRollup(rt.emitter)`** (app.go:988-994) runs BEFORE `shutdownCtx` is created (line 996). `Processor.FlushRollup` (internal/flowlog/processor.go:338) takes no context either: it is a synchronous in-memory emit into the OTEL SDK aggregation. No network I/O, microseconds. Sequential is correct here and needs only a comment saying so.

**The real defect, CONFIRMED.** `sqlitestore.Store.Close()` (internal/flowstore/sqlitestore/schema.go:351) is `close(s.done); s.wg.Wait(); s.db.Close()`. `wg.Wait()` is UNBOUNDED — it blocks until every background worker (retention/vacuum) returns. N tailnet runtimes close sequentially, unbounded, entirely OUTSIDE the budget the deployment grace period is derived from. `worstCaseDrain()` does not know these closes exist, so the only thing covering them is `shutdownBudgetHeadroom` (15s), which exists for process teardown and container overhead, not for N unbounded SQLite closes.

Consequence: on a multi-tailnet deployment with `flows.directory` set, the container can be SIGKILLed mid-close once N closes exceed the headroom — and the drift gate that exists precisely to stop the grace period being too short (`shutdownbudget_test.go`, #332) is blind to it. Data loss is bounded (SQLite WAL replays on next open) but the shutdown-budget contract is silently violated, which is the thing that test was written to prevent.

Wave 1 Lane C3 started by root after C2 commit 175e3ce. Harness Codex; Appendix A route EXECUTION to Luna/max. Lane owns the frozen shutdown close-budget implementation; root retains integrated gate, review, commit, push, external effects, and tracker finalization.

Negative guard evidence: omitting the fifth stage produced 30s rather than 40s; a sequential close scaffold starved the fast runtime; and the pre-regeneration chart guard caught minimum 45 rather than 55. All breaks were restored. Helm render reported 457 passed, 0 failed; just check and CI 33312668201 passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed 2d84e51: close per-tailnet flow stores concurrently under a shared 10-second stage, account for a 40-second drain, and raise deployment grace to 55 seconds. Verified by blocked-runtime race test, Helm renders, full gate, and CI.
<!-- SECTION:FINAL_SUMMARY:END -->
