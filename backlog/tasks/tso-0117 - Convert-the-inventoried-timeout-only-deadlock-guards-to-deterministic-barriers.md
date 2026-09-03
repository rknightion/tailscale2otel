---
id: TSO-0117
title: Convert the inventoried timeout-only deadlock guards to deterministic barriers
status: Done
assignee:
  - '@codex'
created_date: '2026-09-03 05:19'
updated_date: '2026-09-03 13:57'
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
- [x] #1 Every guard in Lane A's inventoried set either waits on a deterministic barrier or is recorded, with its reason, as one that must keep a timeout
- [x] #2 The two load-sensitive cases named above are converted first and are covered by the same negative-test discipline as TSO-0112
- [x] #3 Each conversion is negative-tested: the behaviour under test is broken on purpose, the test is observed to fail for the right reason, and it is restored
- [x] #4 Lane A's not-in-scope classification is respected; no synctest sleep, teardown context or timestamp fixture is converted
- [x] #5 No production code changes; a guard that cannot be converted without one is returned as a finding
- [x] #6 No timeout is merely lengthened, and no test is skipped or retried to make it pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 8 Lane D: use TSO-0112 implementation notes as the fixed inventory; convert the two named load-sensitive guards first, then every included timeout-only guard in the three owned test-file sets to deterministic barriers or record why one must remain; negative-test each conversion, preserve the explicit exclusions, and make no production changes, commits, or pushes.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Commit fbc5920 converted the inventoried ingress-WAL replay, annotation worker readiness, telemetry wire request, and provider first-write waits to deterministic barriers without production changes, retries, skips, or longer timeouts. Negative probes failed correctly for a duplicate ingress-WAL callback, stalled annotation readiness, dropped wire metrics, and missing provider writes. The final uncached race gate passed for internal/ingresswal, internal/annotations, and internal/telemetry. Retained timing bounds are deterministic teardown contexts, the explicitly excluded provider settlement measurement, a synctest-controlled sleep, or timestamp fixtures; those classifications match the fixed TSO-0112 inventory. CodeRabbit completed; two minor suggestions to restore local timeout margins contradicted the acceptance criteria and were left. Final integration at 1c088cea1dbdd9fbcd0d59086953bada2a9ff69f: just check passed; just gen left no diff; just --fmt --check passed; exact-head CI 33762639276 succeeded on attempt 1.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Commit fbc5920 replaces every in-scope timeout-only deadlock guard with a deterministic synchronization barrier, preserves the documented exclusions, and changes no production code. Every conversion was negative-tested and the uncached focused race gate passed.
<!-- SECTION:FINAL_SUMMARY:END -->
