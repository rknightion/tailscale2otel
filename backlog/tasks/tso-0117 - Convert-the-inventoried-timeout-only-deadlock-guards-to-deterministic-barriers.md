---
id: TSO-0117
title: Convert the inventoried timeout-only deadlock guards to deterministic barriers
status: To Do
assignee: []
created_date: '2026-09-03 05:19'
labels: []
dependencies:
  - TSO-0112
priority: medium
type: chore
ordinal: 118000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 7's Lane A inventoried every test in the suite that asserts through a wall-clock margin. Four were fixed under TSO-0112 because they had actually fired. The remainder are generic timeout-only deadlock guards: a helper waits on a channel and a timeout exists only so a hung test fails instead of hanging forever.

Owner decision 2026-09-03: convert them rather than wait for each to bite. The four that fired were not special; they were the ones that happened to lose the race first, and each cost a wave a retry before anyone read it.

The inventory is on TSO-0112's implementation notes and is the input to this task - do not re-derive it. Lane A flagged two as load-sensitive specifically, and they are the natural first targets:

- internal/ingresswal/wal_test.go:1700-1714 - waits on replay and error channels behind a 100 ms timeout
- internal/annotations/annotations_test.go:832-841 - starts an asynchronous publisher, then relies on completion or timeout rather than a second readiness signal

The rest, listed in the same notes, cover internal/telemetry helpers (wirecontract_helpers_test.go:998-1006 and related), internal/telemetry/processors_stdout_test.go:57,86,135,172 shutdown contexts, and the providerset benchmark wait at internal/telemetry/providerset_bench_test.go:249.

Lane A also recorded what is explicitly NOT in scope, and that classification stands: time.Sleep inside testing/synctest.Test advances synthetic time and is not load-sensitive; context.WithTimeout used purely for deterministic teardown is not a margin; historical timestamp fixtures do not wait. Do not convert those.

This is test-only work. A guard that cannot be converted without a production change is a finding to return, not a change to make.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every guard in Lane A's inventoried set either waits on a deterministic barrier or is recorded, with its reason, as one that must keep a timeout
- [ ] #2 The two load-sensitive cases named above are converted first and are covered by the same negative-test discipline as TSO-0112
- [ ] #3 Each conversion is negative-tested: the behaviour under test is broken on purpose, the test is observed to fail for the right reason, and it is restored
- [ ] #4 Lane A's not-in-scope classification is respected; no synctest sleep, teardown context or timestamp fixture is converted
- [ ] #5 No production code changes; a guard that cannot be converted without one is returned as a finding
- [ ] #6 No timeout is merely lengthened, and no test is skipped or retried to make it pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
