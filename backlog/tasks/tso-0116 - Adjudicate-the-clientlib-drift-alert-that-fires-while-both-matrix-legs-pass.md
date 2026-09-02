---
id: TSO-0116
title: Adjudicate the clientlib-drift alert that fires while both matrix legs pass
status: To Do
assignee: []
created_date: '2026-09-02 15:48'
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
- [ ] #1 The report job's verdict-counting logic is stated precisely: what a leg writes when it passes, what it writes when it fails, and what the report does with each
- [ ] #2 The 2026-08-31 alert is adjudicated as either a real upstream break or a false positive, with the evidence
- [ ] #3 If it is a false positive, the aggregation is fixed so a passing leg cannot be counted as a failure, and the fix is negative-tested against both a real failure and a clean run
- [ ] #4 Issue #599 is closed or restated to match the adjudication rather than left open and contradicted
- [ ] #5 The inconclusive path stays distinguishable from green; a leg that dies before writing a verdict must not be reported as passing
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
