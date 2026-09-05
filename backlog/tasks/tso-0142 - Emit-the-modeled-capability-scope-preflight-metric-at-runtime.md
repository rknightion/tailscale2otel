---
id: TSO-0142
title: Emit the modeled capability scope preflight metric at runtime
status: To Do
assignee: []
created_date: '2026-09-05 22:01'
updated_date: '2026-09-05 22:45'
labels: []
dependencies: []
references:
  - internal/app/app.go
  - internal/app/capability.go
  - internal/app/capability_test.go
priority: medium
type: bug
ordinal: 143000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The shipped catalog and dashboard expose `tailscale2otel.capability.scope_satisfied`, and the live admin capability matrix reports modeled OAuth scopes as satisfied, but a trailing 30-day service-scoped presence query found no series. Source inspection shows `EmitScopePreflight` is exercised by unit tests but has no production call site; the heartbeat callback only invokes `EmitCapabilityStatus`. Operators therefore cannot observe a real configured-scope regression through the shipped metric.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A running exporter with modeled OAuth scopes emits one bounded `tailscale2otel.capability.scope_satisfied` series per checkable capability, matching the admin capability matrix
- [ ] #2 Unknown and not-applicable scope states remain absent rather than becoming false permission failures
- [ ] #3 A runtime-level test fails when the production reporter stops invoking scope-preflight emission
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner decision 2026-09-05: bundled into the 2026-09-11 wave with TSO-0133; PR #585 (5.0.0) merges after both land.
<!-- SECTION:NOTES:END -->
