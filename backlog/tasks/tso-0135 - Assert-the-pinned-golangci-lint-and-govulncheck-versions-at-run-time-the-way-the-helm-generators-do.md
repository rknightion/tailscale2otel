---
id: TSO-0135
title: >-
  Assert the pinned golangci-lint and govulncheck versions at run time, the way
  the helm generators do
status: Done
assignee:
  - '@codex'
created_date: '2026-09-04 13:01'
updated_date: '2026-09-05 18:41'
labels: []
dependencies: []
priority: medium
type: chore
ordinal: 136000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
just lint and just vuln run whatever binary is on PATH. The justfile pins golangci_version := "v2.13.2" and govulncheck_version := "v1.3.0" and just setup installs exactly those, but neither recipe checks what it actually invoked, so a clone whose setup predates a pin bump lints against a different rule set than CI enforces and reports a green that means nothing.

Live instance, Wave 11: a machine left on golangci-lint 2.12.2 ran just lint green across all five modules while CI's 2.13.2 failed two SA1019 deprecations in internal/coordination/lease_observer.go (CI run 33868900332). The wave read that as a runner-only failure. Reinstalling the pin and re-running just lint reproduced CI's verdict exactly.

The pattern to copy already exists in this repository. scripts/regen-generated.sh has have_tool <bin> <pinned-version> <install-target>: a missing OR version-mismatched helm tool is a loud SKIP naming the exact fix, deliberately not a hard failure, so the pre-commit hook stays usable on a machine without the toolchain. Lint and vuln differ in one respect from the generators: a skipped generator writes nothing and CI's fail-on-diff still catches it, whereas a skipped lint leaves no downstream trace at all. Decide whether a mismatch here is a SKIP or a hard error, and state the reason.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 just lint and just vuln each verify the invoked binary against the justfile pin before running, and a mismatch names the exact fix command
- [x] #2 The mismatch behaviour (loud skip or hard error) is chosen deliberately and its reason is stated in the recipe, given that an unlinted module leaves no CI fail-on-diff backstop
- [x] #3 A negative test proves the assertion actually fires on a wrong version rather than passing vacuously
- [x] #4 AGENTS.md's task-interface section states that a local lint is only evidence when the pin assertion passed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 13 front-loaded Codex campaign: root owns tracker, config seams, integration, gates and explicit-path commits. Four flat lanes use the frozen goal sections 0, 4, 5 and 6; A/C/D overlap phase 0, B waits for its local commit and C acceptance before option wiring. Verify focused contracts, generated stability, full gate, CodeRabbit, exact-head CI/auto-rc and read-only stdout live runs before reconciliation and terminal file report.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane A focused checks passed: pinned lint reports 0 issues, vuln reports no vulnerabilities, wrong-version and matching-version shims pass, just format/JSON dump pass. Integration found the existing CI recipe parser excludes underscore-prefixed names; helpers renamed while retaining [private], leaving the parser unchanged. Root tightened parsing to compare the complete version token so a prerelease cannot masquerade as a stable pin; added a prerelease rejection case.

Delivered in 0763467 after final integrated just check passed (verbose v6, only the three known skips) and corrected CodeRabbit review completed with zero findings. Per-task commits use explicit staged paths and a one-command hook override because the hook had already pulled an uncommitted generated metric row into phase 0; generation was explicitly run and byte-checked instead of allowing cross-task restaging. No persistent hook configuration changed.

Implementation SHA 0763467afcff9ac9663edf275c1e7f95caa8a4d6. Integrated CI 33984156766 at c94ba92dbfa47f8f99c527cb8fc7ea9a032a8f4f succeeded on attempt 1. Final local gate passed; only the three documented fixture/platform skips occurred. Generated hash stability and just formatting passed. Removing needs-triage because this wave explicitly commissioned and completed the task.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Enforced the invoked lint and vulnerability tool pins with hard failures and repair guidance. Wrong-version/prerelease/matching shims and the full integrated local and CI gates passed.
<!-- SECTION:FINAL_SUMMARY:END -->
