---
id: TSO-0112
title: Eliminate the load-dependent CI test flakes that make the gate unreliable
status: Done
assignee:
  - '@codex'
created_date: '2026-09-02 15:48'
updated_date: '2026-09-03 05:17'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 113000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The CI gate now fails on commits that cannot have caused a failure, with a different test each time. Five runs on 2026-09-02 produced four distinct failing tests:

| run | commit content | failing test |
| --- | --- | --- |
| 33594447451 | backlog markdown only | internal/collector/services TestCollectSkipsHostInfoSnapshotAfterCanceledDispatch |
| 33609767646 | a merge commit | internal/flowstore/sqlitestore TestOpen_ConvertsExistingDatabaseOutsideQueryTimeout |
| 33622363955 | a dependency bump | the same test, plus TestSweepAutomaticallyReclaimsExpiredRows |
| 33621590202 attempt 1 | a shared-workflow pin bump | TestOpen_ConvertsExistingDatabaseOutsideQueryTimeout |
| 33621590202 attempt 2 | the same commit | internal/collector TestSelfObs_SuccessfulSnapshotEmitsScrapeMetrics |

A tracker-only commit cannot break Go tests, so every one of these is a flake rather than a regression. Wave 5 hit the same class in TestSweepAutomaticallyReclaimsExpiredRows and passed by retrying, so the workaround is now two waves old and the retry has become routine.

This is worse than the individual failures. A gate that fails about half the time on unrelated changes trains everyone to retry rather than read, so a genuine regression gets dismissed as noise and a green run stops being evidence.

None of the four reproduce locally. All pass under 'go test -race -count=8' and under GOMAXPROCS=1 on a fast disk, so the shared cause is CI I/O and scheduling pressure with 26 concurrent jobs, not a logic race.

Known mechanisms, to be confirmed rather than assumed:
- TestOpen_ConvertsExistingDatabaseOutsideQueryTimeout writes a 32 MiB blob and allows the auto-vacuum conversion a 5-second ConversionTimeout. The payload only has to exceed the 25 ms QueryTimeout to prove the point, so the margin is far larger than the assertion needs.
- TestSelfObs_SuccessfulSnapshotEmitsScrapeMetrics failed with 'tailscale2otel.scrape.last_timestamp not emitted' at a 0.00s runtime, which points at clock resolution or a change-gated emission rather than slowness. Note that Wave 6 added checkpoint shard tests to the same package that build ~740 KB payloads, so package timing changed underneath it.

Fix the tests so they assert the same behaviour without a wall-clock margin. Do not paper over it with a global retry, a longer timeout chosen by guesswork, or by skipping under CI.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Each of the four named tests asserts the same behaviour with no dependence on how fast the runner's disk or scheduler is
- [x] #2 Every timeout or size chosen to make a test deterministic is justified in a comment against what the assertion actually needs to prove
- [x] #3 Each fix is negative-tested: the behaviour under test is broken on purpose, the test is observed to fail, and it is restored
- [x] #4 Any test made deterministic by a fake clock uses testing/synctest, consistent with the existing heartbeat tests, rather than a real sleep
- [x] #5 The mechanism behind each failure is stated as a confirmed finding or explicitly recorded as unconfirmed; no fix lands on an assumed cause
- [x] #6 No global test retry, no CI-only skip, and no blanket timeout increase is introduced
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Dispatch Lane A read-only suite sweep and Lanes B/C/D as disjoint test-only implementations under the frozen ownership map, with at most three live lanes.
2. Require each implementation lane to confirm or disprove its stated mechanism, remove wall-clock scheduling or disk-speed margins without widening timeouts, and negative-test the repaired assertion.
3. Integrate and inspect all lane changes; resolve only the two root-return seams (production code or internal/telemetrytest) if encountered.
4. Run focused package checks, CodeRabbit before each code commit, build-check each explicit-pathspec commit, then run the integrated just check gate.
5. Commit and push the code and tracker updates directly to main using explicit pathspecs only.
6. After the fix is on main, trigger exactly one fresh rerun for PR #605, record its run ID/conclusion/attempt count, finalize TSO-0112 in one edit call, and leave PRs #605 and #585 open.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Lane A - suite-wide wall-clock-margin inventory

Read-only sweep at `f3fc4548e6952920a02ce3486a972e1a447d287e`: 549 `*_test.go` files across the root and nested Go modules. Searched real sleeps, `time.After`, `context.WithTimeout`, deadline loops, timers/tickers, goroutine/channel synchronization, duration assertions, and large count/size loops.

### Shape 1: real `time.Sleep`

Load-sensitive:

- `internal/credreload/credreload_shutdown_test.go:29` - `TestStop_NoGoroutineLeak`, 200µs scheduling margin.
- `internal/credreload/credreload_shutdown_test.go:62` - `goroutineCountStable`, 1ms polling/scheduling margin.
- `internal/credreload/credreload_poller_test.go:40` - `TestPoller_PicksUpRotationOnTick`, real one-minute tick wait.
- `internal/stream/stream_test.go:996` - `TestRun_GracefulShutdown`, 20ms listener-start polling interval.
- `internal/telemetry/processors_stdout_test.go:182` - `TestNewProvider_StdoutMetricIntervalDefaultsShort`, real 6s wait for metric emission; highly load-sensitive.
- `internal/telemetry/processors_batch_test.go:339` - `TestNewProvider...NoGoroutineLeak`, 20ms teardown margin.
- `internal/telemetry/exporters_transport_test.go:160` - `TestTransport_TimeoutBoundsExportAttempt`, exporter server sleeps 2s to exercise timeout; intentionally slow and load-sensitive.
- `internal/telemetry/providerset_bench_test.go:140` - benchmark/helper path, 20ms scheduling margin.
- `internal/telemetry/providerset_bench_test.go:249` - benchmark/helper path, `3*interval + 100ms` real wait; load-sensitive.
- `internal/collector/nodemetrics/nodemetrics_test.go:544` - `slowServer`, sleeps caller-selected delay; used by duration/concurrency tests and load-sensitive.
- `internal/collector/objectstore/requests_test.go:103,108` - slow provider test double; sleeps configured delay to make request duration measurable; load-sensitive.
- `internal/collector/selfobs_test.go:316,344` - disabled-collector tests, 20ms post-cancellation margin; load-sensitive.
- `internal/app/dedupobs_test.go:76` - `TestRunDedupReporter_EmitsSizeAndEvictionDeltas`, one-minute reporter tick; load-sensitive.
- `internal/app/metrics_concurrency_test.go:80` - `waitForInFlight`, 1ms polling interval; load-sensitive.
- `internal/app/metrics_concurrency_test.go:188,242` - coalesced/default scrape tests, 100ms settle margin; load-sensitive.
- `internal/app/tls_handshake_test.go:101,167` - TLS listener polling, 10ms retry interval; load-sensitive.
- `internal/app/ingresswal_test.go:989` - coordinator readiness polling, 1ms interval; load-sensitive.
- `internal/app/listenerhealth_test.go:125` - graceful-shutdown test, 50ms margin before checking completion; load-sensitive.
- `internal/app/admin_rdns_test.go:49` - `seedRDNS`, 1ms polling interval; load-sensitive.
- `internal/app/catalog_test.go:37` - self-observation startup polling, 1ms interval; load-sensitive.
- `internal/tsapi/ratelimit_test.go:73` - rate-limit retry timing, 5ms sleep; load-sensitive.
- `internal/collector/scheduler_test.go:74` - scheduler `waitFor` helper, 1ms polling interval; load-sensitive.
- `internal/webhook/tls_test.go:72` - TLS listener polling, 10ms interval; load-sensitive.
- `internal/flowstore/sqlitestore/integration_test.go:27` - persistence polling, 10ms interval; load-sensitive.
- `internal/flowstore/sqlitestore/writer_test.go:90` - row polling helper, 5ms interval; load-sensitive.
- `internal/flowstore/sqlitestore/writer_test.go:618,634` - automatic reclaim/checkpoint polling, 10ms/5ms intervals; load-sensitive.
- `internal/certreload/certreload_test.go:155,197,286,309,329` - `MinRecheckInterval` sleeps. These are real sleeps despite nearby fake-clock coverage; load-sensitive and potentially very slow depending on the constant.

Not wall-clock: `internal/geoip/updater_test.go:52,57,85,119,166`; `internal/rdns/stale_test.go:26,53,98,134,170,209,215,241,266,290,296,328,368`; `internal/rdns/rdns_test.go:125`; `internal/rdns/rdns_metrics_test.go:90`; `internal/app/confighealth_test.go:133,165`; `internal/app/tick_emission_test.go:49,73`; `internal/app/heartbeat_test.go:51`; and `internal/collector/selfobs_test.go:405,417` all run under `synctest.Test` and advance synthetic time.

### Shape 2: polling helper with a deadline

- `internal/collector/scheduler_test.go:69-75` - `waitFor`; polls scheduler state until `time.Now().Add(timeout)`; load-sensitive.
- `internal/flowstore/sqlitestore/integration_test.go:22-28` - `waitForPersistedRows`; polls SQLite rows for 10s; load-sensitive.
- `internal/flowstore/sqlitestore/writer_test.go:82-91` - `waitForRows`; polls row count for caller timeout; load-sensitive.
- `internal/flowstore/sqlitestore/writer_test.go:613-619` - automatic reclaim polling; 3s deadline; load-sensitive.
- `internal/flowstore/sqlitestore/writer_test.go:625-635` - checkpoint completion polling; 1s deadline; load-sensitive.
- `internal/app/tls_handshake_test.go:93-102,159-168` - HTTP/TLS listener polling for 3s; load-sensitive.
- `internal/app/metrics_concurrency_test.go:72-82` - `waitForInFlight`; polls gauge for 5s; load-sensitive.
- `internal/app/admin_rdns_test.go:44-50` - `seedRDNS`; polls cache population for 2s; load-sensitive.
- `internal/app/catalog_test.go:32-38` - polls self-observation metric for 2s; load-sensitive.
- `internal/app/ingresswal_test.go:983-990` - polls coordinator readiness for 2s; load-sensitive.
- `internal/stream/stream_test.go:984-997` - polls server acceptance for 3s; load-sensitive.
- `internal/webhook/tls_test.go:61-73` - polls TLS server acceptance for 3s; load-sensitive.

### Shape 3: `context.WithTimeout` sized as an operational margin

Load-sensitive or timing-contract-sensitive:

- `cmd/tailscale2otel/healthcheck_test.go:202` - 50ms request timeout contract.
- `internal/release/tracing_test.go:113` - 20ms cancellation/deadline propagation contract.
- `internal/hsapi/tracing_test.go:145` - 20ms cancellation/deadline propagation contract.
- `internal/tsapi/transport_test.go:417` - 20ms transport timeout contract.
- `internal/tsapi/ratelimit_test.go:120` - 5ms request timeout around rate limiting.
- `internal/telemetry/exporters_transport_test.go:134,181,221,256,271` - 1s shutdown/export-operation budgets; `:188-195` and `:228-237` additionally assert elapsed wall time against those margins.
- `internal/telemetry/processors_stdout_test.go:57,86,135,172` - 5s shutdown budgets; `:182` adds the real 6s interval wait.
- `internal/telemetry/processors_batch_test.go:72,116,223,289,328` - 5s export/shutdown budgets.
- `internal/telemetry/provider_batching_test.go:62` - 1s shutdown budget.
- `internal/telemetry/shutdown_internal_test.go:89,152,199,227` - 2s/100ms shutdown budgets; `:230-233` asserts elapsed wall time.
- `internal/telemetry/credrotation_test.go:68,127` - 5s shutdown/export budgets.
- `internal/telemetry/wirecontract_helpers_test.go:946` - 5s export budget.
- `internal/telemetry/wirecontract_http_test.go:139,172,221` - 5s export budgets.
- `internal/telemetry/wirecontract_grpc_test.go:154,187,220` - 5s export budgets.
- `internal/app/listenerhealth_test.go:56,76` - 5s server startup/shutdown budgets.
- `internal/app/preflight_test.go:139,255,337,376,382,420` - 5s/10s lifecycle and budget contexts.
- `internal/app/app_test.go:740,801` - 150ms/5s app lifecycle contexts.
- `internal/app/tick_emission_test.go:111` - 100ms shutdown context.
- `internal/app/multitailnet_receivers_test.go:100` - 150ms receiver lifecycle context.
- `internal/flowstore/sqlitestore/writer_test.go:241,273,534` - 1s database operation contexts.
- `internal/stream/hardening_internal_test.go:97` - 100ms deadline propagation contract.
- `internal/coordination/coordination_test.go:91,97,104,112,143,151` - 50ms/200ms/2s coordination response margins.
- The telemetry processor and shutdown tests above also use `time.After` guards at the matching 1s/5s locations.

### Shape 4: size/count chosen to make an operation measurable

- `internal/annotations/annotations_test.go:832-841` - `TestPublishNeverBlocks` publishes 10,000 events and uses a 10s timeout to observe non-blocking behavior; count and timeout are load-sensitive.
- `internal/telemetry/providerset_bench_test.go:249` - waits `3*interval + 100ms` after a measurable interval; load-sensitive.
- `internal/telemetry/processors_stdout_test.go:182` - waits 6s so the default metric interval must visibly elapse; load-sensitive.
- `internal/collector/objectstore/requests_test.go:103,108` - configured delay makes request-duration histogram buckets measurable; load-sensitive.
- `internal/collector/nodemetrics/nodemetrics_test.go:544` - configured slow-server delay makes per-target duration/concurrency behavior measurable; load-sensitive.
- `internal/app/metrics_concurrency_test.go:188,242` - 100ms settle period makes coalescing state observable; load-sensitive.

### Shape 5: waits for signal A, then reads B without an explicit wait for B

Potential candidates inspected:

- `internal/app/metrics_concurrency_test.go:78-80` - waits for in-flight gauge A, then immediately reads `slow.calls` B at `:80`; load-sensitive.
- `internal/app/metrics_concurrency_test.go:188-190` and `:242-244` - waits for in-flight state, then after a fixed 100ms reads collector call count; load-sensitive.
- `internal/collector/devices/concurrency_test.go:69-114` - waits for started/release signals, then immediately reads completion/error state; channel barriers mostly make this deterministic, but timeout branches are load-sensitive.
- `internal/collector/services/concurrency_test.go:57-101` - same pattern as devices concurrency tests; channel barriers mostly deterministic, timeout branches load-sensitive.
- `internal/telemetry/shutdown_internal_test.go:109-117,158-169,205-212` - waits for exporter-start signals then checks other completion channels; explicit timeout guards are load-sensitive.
- `internal/telemetry/provider_forceflush_internal_test.go:159-169,196-203,246-253,309-313` - waits for force-flush-start signal then observes completion; timeout guards are load-sensitive.
- `internal/stream/hardening_internal_test.go:102-113` - waits for callback completion after deadline signal; channel synchronization is explicit, timeout branch is load-sensitive.
- `internal/stream/stream_test.go:980-1013` - waits for server acceptance via successful request, then checks metrics and shutdown completion; shutdown `time.After(5s)` is load-sensitive.
- `internal/webhook/tls_test.go:53-81` - waits for successful TLS response, then checks shutdown completion; 5s timeout is load-sensitive.
- `internal/webhook/webhook_test.go:616-626` - cancels and immediately waits for `Run`; 5s timeout is load-sensitive.
- `internal/coordination/coordination_test.go:75-151` - signal channels establish lifecycle points, then subsequent state/error reads rely on timeout-guarded ordering; load-sensitive.
- `internal/ingresswal/wal_test.go:1700-1714` - waits on replay/error channels and uses a 100ms timeout; load-sensitive.
- `internal/annotations/annotations_test.go:832-841` - starts asynchronous publisher, then relies on completion/timeout rather than a second readiness signal; load-sensitive.

### Explicitly not classified as wall-clock-margin defects

- `time.Sleep` inside `testing/synctest.Test`: all listed geoip, rdns, certreload rate-limit, app/confighealth, app/tick_emission, app/heartbeat, and collector/selfobs fake-clock sleeps. They advance synthetic time and do not depend on host load.
- `context.WithTimeout` used solely as a cancellation context for deterministic teardown where the test does not assert elapsed time or rely on the timeout to make the expected operation complete.
- `time.After` in negative-path tests whose purpose is to fail rather than wait, when the tested operation is explicitly blocked on a release channel and the timeout is only a deadlock guard.
- Historical timestamp fixtures using `time.Now().Add(...)` in flowlog, collector, webhook, config, and related tests; these compare synthetic timestamps and do not wait.
- Large loops used for cardinality, parsing, allocation, fuzz, or data-shape coverage where no elapsed-time assertion or synchronization margin is made.
- Benchmark-only sleeps and benchmark data sizes where the benchmark itself is not a normal test assertion, except the explicitly noted providerset benchmark wait at `internal/telemetry/providerset_bench_test.go:249`.
- `time.After`/timeout guards in `internal/telemetry/wirecontract_helpers_test.go:998-1006` and related helpers where the helper waits on a recorder channel and the timeout is a generic deadlock guard; retain as candidates if future work treats all timeout guards as in scope.
- `internal/telemetry/processors_stdout_test.go:57,86,135,172` and similar 1s/5s shutdown contexts are operational safety bounds, not necessarily margins sized merely to be enough unless the completion assertion is treated as the contract.
- `internal/collector/selfobs_test.go:405,417` fake-clock sleeps are deliberately exact duration injection, not wall-clock timing.
- `internal/geoip/updater_test.go:52,57,85,119,166` and all `internal/rdns/stale_test.go` sleeps are synthetic-time advancement, not load-sensitive.

Uncovered classification question retained for the mandatory final questions section: whether generic timeout-only deadlock guards should be included in a future remediation set. They are inventoried above, but many are synchronization safety nets rather than assertions whose correctness depends on elapsed time.

## Confirmed mechanisms for the four commissioned failures

- `TestOpen_ConvertsExistingDatabaseOutsideQueryTimeout`: confirmed. The old test made a 32 MiB VACUUM cross a 25 ms query timeout on a fast local disk, then assumed a 5 s conversion budget was enough on every runner. The production invariant is context lineage, not elapsed duration: `configureIncrementalAutoVacuum` creates the VACUUM context from `context.Background()` with the conversion deadline. The replacement fake SQL driver observes that directly with no disk or scheduler margin.
- `TestSweepAutomaticallyReclaimsExpiredRows`: confirmed. The old test waited on 20 ms real maintenance ticks, polled for up to 3 s, and required asynchronous delete, incremental vacuum, WAL checkpoint, and filesystem-size change to all win that wall-clock race. A first maintenance tick deletes and vacuums into WAL; a second checkpoints the reduced database. The replacement advances exactly two synthetic ticks under `testing/synctest`.
- `TestSelfObs_SuccessfulSnapshotEmitsScrapeMetrics`: confirmed. The old test synchronized only on `scrape.success`, then immediately read duration and `scrape.last_timestamp`; `emitScrapeMetrics` emits last timestamp after success. Calling synchronous `RunTick` makes completion of all emissions the barrier.
- `TestCollectSkipsHostInfoSnapshotAfterCanceledDispatch`: confirmed. The dispatcher may take its canceled `Done` select arm without invoking the wrapped context `Err`, so the old `dispatchConfirmed` observation had no guaranteed happens-before. The replacement blocks the second pre-dispatch `Err` check until cancellation and separately detects a bypassed second dispatch, with channels rather than a timeout.

## Wave 7 integration and verification

Code commit: `231d43505849e6fce5aceb38d0de3ab219cc5a79` (`test(ci): make timing-sensitive tests deterministic`). Modified only `internal/collector/selfobs_test.go`, `internal/collector/services/services_test.go`, and `internal/flowstore/sqlitestore/writer_test.go`; production code and `internal/telemetrytest` were unchanged.

Negative-test evidence, with each mutation restored before integration:

- SQLite conversion: `writer_test.go:473: VACUUM inherited the query context marker "query timeout"`.
- SQLite sweep: `writer_test.go:685: rows after automatic sweep = 500, want 0`.
- Self-observation: `selfobs_test.go:101: tailscale2otel.scrape.last_timestamp not emitted`.
- Services cancellation: `services_test.go:273: partial host.info snapshot = [...] want no snapshot`.

Local verification:

- All four commissioned tests passed after restoration.
- Full affected packages passed under `-race`.
- `just --fmt --check` exited 0.
- `just build` exited 0 against the squashed code commit before push.
- `just check` completed successfully after removing one unused test helper: all root and tool modules, generated-artifact drift checks, vulnerability checks, 127 generator tests, 46 script tests, 466 Helm cases, and 71 Compose cases passed.
- `just gen` was not run because no generated-artifact input changed; this is a conditional skip, not a pass.

CodeRabbit review completed before the code commit. It reported no code finding. Two tracker findings conflicted with the frozen run contract and were left as false positives; one valid tracker omission was fixed by appending the four confirmed mechanisms above. CodeRabbit was skipped for the forthcoming tracker-only commit, as required for tracker-only changes.

Exact-head main CI run `33656063229` completed `success` on attempt 1 for `231d43505849e6fce5aceb38d0de3ab219cc5a79`.

## PR #605 reachability check

Exactly one authorized post-fix rerun was issued for run `33621590202`. It completed `failure` as GitHub attempt 3, which was the first and only post-fix reachability attempt. No second post-fix attempt was issued.

Verbatim commissioned-test failures from that attempt:

- `writer_test.go:407: Open converted database: sqlitestore: read tailnet identity: context deadline exceeded`
- `writer_test.go:602: timed out waiting for 500 rows, have 144`

The attempt did not exercise the fix: checkout fetched merge commit `53eac5a136d0e0b93c04ff1d9e06aa5601a32e52`, whose parents are PR head `7d972f3f44438d8185ba4be6911692aeb1e3a8e5` and stale base `56c046e0219fa215da115fdcebae66b9e1b6b61f`. The current fixed main SHA was `231d43505849e6fce5aceb38d0de3ab219cc5a79`. Therefore the required fresh first-attempt PR proof is not established and the wave has not succeeded, even though exact-head main CI is green. Resume boundary: refresh PR #605 onto current main by a separately authorized mechanism, then run one genuinely fresh CI attempt containing `231d435`; do not treat another rerun of the frozen run as evidence.

## Questions for Rob

1. Should the generic timeout-only deadlock guards inventoried by Lane A be commissioned as a future remediation set, or remain accepted safety bounds unless one is tied to a real flake?
2. Lane B returned partial, so root took the narrowest reversible option and used a fake SQL-driver context probe for the conversion invariant. Should that probe remain the preferred pattern for similar context-lineage tests?
3. PR #605 reruns preserve its stale pre-fix merge commit. Which separately authorized refresh mechanism should be used to obtain a genuinely fresh PR run containing current main: update the Renovate branch, recreate the PR merge ref another way, or replace the reachability check?

Resume boundary discharged 2026-09-03 by the root session, not by another wave.

The parked reason was correct and could not be cleared by any rerun: 'gh run rerun' replays a frozen merge commit, and PR #605's was 53eac5a, whose base parent is the pre-fix 56c046e. No number of attempts on run 33621590202 could ever contain 231d435.

The mechanism that does work is 'gh pr update-branch 605', which rebuilds the PR head against current main. Head moved 7d972f3 -> 2bafaed, and GitHub started a genuinely fresh CI run 33683276610 on it.

Result: 26 checks pass, 2 skipping, zero failures, on the FIRST attempt, with 231d435 present. 'build / vet / test' passed in 8m36s - the job that had failed on three previous attempts across two different tests.

That satisfies the reachability criterion as written: a fresh first-attempt green PR run containing the fix. Combined with exact-head main CI 33656063229 (success, attempt 1) at 231d435, the gate is evidence again.

DoD #2 stays unchecked deliberately: no generated artifact's inputs changed, so 'just gen' was correctly not run. That is a conditional skip, recorded as a skip rather than a pass.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented and negative-tested deterministic test-only fixes in 231d43505849e6fce5aceb38d0de3ab219cc5a79; just check passed and exact-head main CI run 33656063229 succeeded on attempt 1. The required PR #605 reachability run 33621590202 attempt 3 failed because GitHub reused frozen merge commit 53eac5a based on pre-fix 56c046e, so it did not exercise the fix. Parked pending a separately authorized genuinely fresh PR run containing current main.
<!-- SECTION:FINAL_SUMMARY:END -->
