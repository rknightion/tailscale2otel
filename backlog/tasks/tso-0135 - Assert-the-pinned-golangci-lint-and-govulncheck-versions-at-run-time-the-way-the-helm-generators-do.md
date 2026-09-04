---
id: TSO-0135
title: >-
  Assert the pinned golangci-lint and govulncheck versions at run time, the way
  the helm generators do
status: To Do
assignee: []
created_date: '2026-09-04 13:01'
labels:
  - needs-triage
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
- [ ] #1 just lint and just vuln each verify the invoked binary against the justfile pin before running, and a mismatch names the exact fix command
- [ ] #2 The mismatch behaviour (loud skip or hard error) is chosen deliberately and its reason is stated in the recipe, given that an unlinted module leaves no CI fail-on-diff backstop
- [ ] #3 A negative test proves the assertion actually fires on a wrong version rather than passing vacuously
- [ ] #4 AGENTS.md's task-interface section states that a local lint is only evidence when the pin assertion passed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
