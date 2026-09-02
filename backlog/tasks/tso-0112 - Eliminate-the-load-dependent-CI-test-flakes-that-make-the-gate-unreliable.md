---
id: TSO-0112
title: Eliminate the load-dependent CI test flakes that make the gate unreliable
status: To Do
assignee: []
created_date: '2026-09-02 15:48'
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
- [ ] #1 Each of the four named tests asserts the same behaviour with no dependence on how fast the runner's disk or scheduler is
- [ ] #2 Every timeout or size chosen to make a test deterministic is justified in a comment against what the assertion actually needs to prove
- [ ] #3 Each fix is negative-tested: the behaviour under test is broken on purpose, the test is observed to fail, and it is restored
- [ ] #4 Any test made deterministic by a fake clock uses testing/synctest, consistent with the existing heartbeat tests, rather than a real sleep
- [ ] #5 The mechanism behind each failure is stated as a confirmed finding or explicitly recorded as unconfirmed; no fix lands on an assumed cause
- [ ] #6 No global test retry, no CI-only skip, and no blanket timeout increase is introduced
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
