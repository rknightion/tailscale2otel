---
id: TSO-0096
title: Shard the CodeRabbit pre-commit review so a wave-sized diff can be reviewed
status: In Progress
assignee: []
created_date: '2026-08-31 10:55'
updated_date: '2026-09-01 18:01'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: high
ordinal: 97000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Wave 3 run reported that its required CodeRabbit reviews "repeatedly failed before analysis with WebSocket closed and no complete line". Reproduced 2026-08-31: `coderabbit review --agent --base <wave base>` over the 270-file Wave 3 diff fails at connecting_to_review_service every time with {"type":"error","errorType":"connection","message":"Connection failed: WebSocket closed"}. The SAME review scoped with --dir succeeds. It is a diff-size limit, not a service outage, and it silently cost seven of eight Wave 3 commits their review.

Sharding found two real defects that shipped unreviewed (a flow-store leak on four early returns in App.New, and a path traversal in organizationTailnetsURL), both fixed in 2167354.

Sharding also has a KNOWN FALSE-POSITIVE CLASS that must be documented alongside the recipe, or it will waste more time than it saves: --dir hides the rest of the repo, so any call site outside the reviewed directory reads as missing. Four of five majors in the post-Wave-3 pass were this artifact - two claimed flowstore.Backend lacked fields that store.go:74-80 defines, one claimed WithProbeIntervals was unwired when collectors.go:278 wires it, one claimed the checkpoint flush was missing when flushCheckpointStores covers both stores on the shutdown path. Every "symbol or wiring is missing" finding from a sharded run must be checked against the whole tree before it is actioned.

Deliver a just recipe that shards by directory, aggregates the NDJSON, and fails when any shard lacks a complete line - because a shard that dies at connect currently looks identical to a clean one.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A just recipe reviews a wave-sized diff by sharding, and its output distinguishes clean from failed per shard
- [ ] #2 A shard with no complete line fails the recipe rather than being counted as clean
- [ ] #3 The false-positive class from --dir scoping is documented where the next agent will read it before triaging findings
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add a documented just review recipe backed by a deterministic shard runner. 2. Test clean, finding, and missing-complete outcomes with a fake CodeRabbit command, including a negative test of the completion guard. 3. Run focused checks and return exact evidence; root integrates, reviews, commits, and pushes.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
- Implemented `just review-sharded` with one CodeRabbit invocation per directory, ordered raw-NDJSON aggregation, a structured completion sentinel, a fail-closed missing-sentinel path, a 15-minute per-shard timeout, and a secure unique default output file.
- Documented and emitted the `--dir` missing-symbol/missing-wiring false-positive warning.
- Negative-tested the completion guard by removing it and observing `test_missing_complete_event_fails_the_review` fail, then restored it.
- Live CodeRabbit review found and drove two initial fixes (predictable `/tmp` output and no timeout), then a third timeout-path fix: `TimeoutExpired` partial output can be bytes despite text mode. Added a failing regression test, observed the TypeError, normalized byte output with replacement-safe UTF-8 decoding, and reran green.
- Final CodeRabbit shard review completed with 0 findings; aggregate path was ephemeral and remains outside the repository.
- `just check` passed: 67 repository checks, 0 failed, including 46 script tests.
<!-- SECTION:NOTES:END -->
