---
id: TSO-0121
title: >-
  Guard the unsuffixed OpenTelemetry instrumentation scope against a future
  major
status: To Do
assignee: []
created_date: '2026-09-03 23:02'
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
- [ ] #1 A test fails if scopeName gains a version suffix or otherwise changes, and its failure message says why the value must stay stable
- [ ] #2 The test is negative-tested: the value is changed on purpose, the test is watched failing, and the change is reverted
- [ ] #3 The comment on the constant states that the omitted major suffix is deliberate, so it reads as a decision rather than an oversight
- [ ] #4 Any other identifier that a module-path major bump could plausibly be expected to change is checked in the same pass, and either guarded or recorded as not applicable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
