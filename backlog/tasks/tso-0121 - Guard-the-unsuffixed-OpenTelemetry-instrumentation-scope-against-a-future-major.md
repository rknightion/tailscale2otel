---
id: TSO-0121
title: >-
  Guard the unsuffixed OpenTelemetry instrumentation scope against a future
  major
status: Done
assignee:
  - '@codex'
created_date: '2026-09-03 23:02'
updated_date: '2026-09-04 07:10'
labels:
  - needs-triage
dependencies: []
priority: medium
type: bug
ordinal: 122000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/telemetry/provider.go:26 declares the instrumentation scope as a hardcoded literal: const scopeName = "github.com/rknightion/tailscale2otel". It carries no major-version suffix, and the v5 module path move correctly left it alone because it is a string constant rather than something derived from the module path.

That is the right value and nothing enforces it. The Go convention for an instrumentation scope is the module path, which for a v2+ module includes the /vN suffix, so the next agent that notices the mismatch has a plausible-sounding reason to 'fix' it. Doing so changes otel_scope_name on every emitted metric and log, which silently breaks any dashboard panel, alert rule or recording rule that filters on it. Nothing in the repository would go red: the artifacts still parse, the panels still load, they just return no data. That is the same failure shape as a renamed metric, which this repository already has a guard test for.

The value must stay stable across majors because the surfaces querying it are operator-facing and long-lived. A scope that moves every major would require every consumer to rewrite their queries at each upgrade for no benefit.

The comment above the constant states what the scope is but not that its lack of a suffix is deliberate, and no test asserts it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A test fails if scopeName gains a version suffix or otherwise changes, and its failure message says why the value must stay stable
- [x] #2 The test is negative-tested: the value is changed on purpose, the test is watched failing, and the change is reverted
- [x] #3 The comment on the constant states that the omitted major suffix is deliberate, so it reads as a decision rather than an oversight
- [x] #4 Any other identifier that a module-path major bump could plausibly be expected to change is checked in the same pass, and either guarded or recorded as not applicable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Inventory module-path-shaped telemetry identifiers and decide which require stability guards.
2. Add a focused test and comment pinning the deliberately unsuffixed instrumentation scope.
3. Negative-test the guard by changing scopeName, observing the expected failure, and reverting.
4. Run focused telemetry checks; root owns integrated review, full gate, commit, push, CI, and finalization.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane implementation evidence: added a stable-scope guard and clarified the constant comment. The focused test passed, a temporary `/v5` scope mutation failed with `operator queries depend on this value remaining stable across module major versions`, and the intended value was restored before the focused test passed again. Inventory result: scopeName is the only telemetry identifier plausibly mistaken for the Go module path; metric, resource and semantic-convention identifiers are module-major independent. Existing repository module-path guards cover root and replace-backed tool modules; promqlcheck is compile-time-only and not a telemetry identifier. Full integrated gate remains root-owned.

Integrated as 86f4b5e6. A temporary /v5 mutation produced the expected stability failure and was restored before the focused test passed. The cumulative full gate passed at 0e212ab5; no generated input changed, and formatting passed. Exact-head CI run 33844779329 attempt 1 succeeded for 630b1d75 before the later fallback fix. The previously missed /v5 rewrite review was also completed in four bounded CodeRabbit slices over all 582 changed internal files with zero findings.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Pinned the deliberately unsuffixed OpenTelemetry instrumentation scope with a decision comment and a negative-tested guard whose failure explains the operator-query stability contract. The identifier inventory found no other module-major-shaped telemetry identifier requiring a guard.
<!-- SECTION:FINAL_SUMMARY:END -->
