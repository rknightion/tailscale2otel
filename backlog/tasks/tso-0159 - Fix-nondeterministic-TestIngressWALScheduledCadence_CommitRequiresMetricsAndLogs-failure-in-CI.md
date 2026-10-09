---
id: TSO-0159
title: >-
  Fix nondeterministic
  TestIngressWALScheduledCadence_CommitRequiresMetricsAndLogs failure in CI
status: Done
assignee:
  - '@loop19'
created_date: '2026-10-09 18:45'
updated_date: '2026-10-09 19:58'
labels:
  - wal
  - ci
dependencies: []
priority: high
type: bug
ordinal: 159000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
CI run 37974335505 on main 303616b9 failed build/vet/test in TestIngressWALScheduledCadence_CommitRequiresMetricsAndLogs/1m0s/cumulative at ingresswal_cadence_test.go:1126 'original admission: HTTP 503' (after a passing devices-sample assertion at :688). The same test passed on CI for 54915a36 and in local gates. Either the test has a nondeterministic dependency (real network/time inside a synctest bubble, shared state across subtests) or the landed TSO-0148 coordinator can refuse an admission (503) it should accept. Main is red until fixed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Root cause identified with evidence (test bug vs production defect)
- [x] #2 The test passes reliably: go test -race -count=50 -run TestIngressWALScheduledCadence_CommitRequiresMetricsAndLogs ./internal/app/ with no failure, including under CPU contention
- [x] #3 If production code was at fault, a regression test pins the corrected behaviour; no assertion weakened
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop19: loop-created repair for main red; 1 implementation attempt; root cause = cadence fixture returned before the WAL worker's first pass (test bug; the 503 is the intended fail-closed re-check). Reproduced 1/23 on Linux 2-CPU and deterministically; fix 50/50 there. Landed 6c40bd81; security review approve; CodeRabbit 0; composed just check green; ci-success green.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Cadence fixtures now synctest.Wait() after starting the WAL worker so its first pass cannot race the poisoned-original admission; no assertion changed. Landed 6c40bd81.
<!-- SECTION:FINAL_SUMMARY:END -->
