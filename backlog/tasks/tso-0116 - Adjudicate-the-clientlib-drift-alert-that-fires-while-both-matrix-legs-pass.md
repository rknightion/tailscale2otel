---
id: TSO-0116
title: Adjudicate the clientlib-drift alert that fires while both matrix legs pass
status: Done
assignee:
  - '@codex'
created_date: '2026-09-02 15:48'
updated_date: '2026-09-03 13:57'
labels: []
dependencies: []
priority: low
type: bug
ordinal: 117000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
GitHub issue #599, open since 2026-08-31, says tailscale-client-go/v2 breaks our build. The evidence does not support it and the alert is probably spurious.

In the run it cites (33402029286) both matrix jobs concluded success - build-against (latest) and build-against (main) - and only the aggregating report job failed. The report claimed '1 of 2 matrix legs failed' after finding exactly one verdict artifact, clientlib-verdict-latest. The requested ref 'latest' also resolved to github.com/tailscale/tailscale-client-go/v2 v2.0.0-20250129222324-74c8fc3cb4d7, which is the pseudo-version the root module already pins, so a genuine break there would break main's own CI. Main's CI is green.

Two possibilities and the task is to distinguish them, not to guess: the legs upload a verdict unconditionally and the report miscounts a green verdict as a failure, or a leg's build step really did exit non-zero while its job still concluded success by design and the aggregation is right. The issue's embedded log excerpt is truncated in a way that shows neither.

This matters more than one stale issue. An advisory lane that cries wolf gets ignored, and this one exists to catch a real upstream break in a dependency with no tagged releases.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The report job's verdict-counting logic is stated precisely: what a leg writes when it passes, what it writes when it fails, and what the report does with each
- [x] #2 The 2026-08-31 alert is adjudicated as either a real upstream break or a false positive, with the evidence
- [x] #3 If it is a false positive, the aggregation is fixed so a passing leg cannot be counted as a failure, and the fix is negative-tested against both a real failure and a clean run
- [x] #4 Issue #599 is closed or restated to match the adjudication rather than left open and contradicted
- [x] #5 The inconclusive path stays distinguishable from green; a leg that dies before writing a verdict must not be reported as passing
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 8 Lane C: establish the exact verdict artifact contract from run 33402029286 and the current workflow/action; correct aggregation so pass, fail, and missing/inconclusive remain distinct; negative-test clean and real-failure cases; return any required workflow-contract guard update to root ownership and do not mutate issue #599, commit, or push.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Verdict contract after commit 37c941b: a completed zero-exit leg writes verdict=pass; a nonzero leg writes verdict=fail only when the requested dependency changed or failed to resolve; a nonzero leg against the unchanged pinned dependency writes verdict=inconclusive. Missing, malformed, or explicit inconclusive verdicts, or a matrix needs result other than success, make the report inconclusive and fail it without opening or closing a drift issue. Two passes produce green and resolve the issue; one or more explicit failures produce one combined report. Run 33402029286 was a false positive: latest resolved to the already-pinned pseudo-version and the failure was the timeout guard later repaired by TSO-0112. The clean, explicit-failure, missing, and unchanged-dependency cases passed; deliberately removing missing-verdict classification failed with FAIL: missing verdict was accepted as green. Issue 599 was closed with this evidence. CodeRabbit completed with zero findings. Final integration at 1c088cea1dbdd9fbcd0d59086953bada2a9ff69f: just check passed; just gen left no diff; just --fmt --check passed; exact-head CI 33762639276 succeeded on attempt 1.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Commit 37c941b replaces artifact-count inference with explicit pass, fail, and inconclusive verdict aggregation, preserves the missing-verdict fail-closed path, and negative-tests clean and real-failure behavior. Issue 599 is closed as a false-positive client-library drift report.
<!-- SECTION:FINAL_SUMMARY:END -->
