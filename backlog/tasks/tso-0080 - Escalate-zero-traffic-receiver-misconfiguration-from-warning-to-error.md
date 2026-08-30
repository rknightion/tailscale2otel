---
id: TSO-0080
title: Escalate zero-traffic receiver misconfiguration from warning to error
status: To Do
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-30 18:33'
labels: []
milestone: m-6
dependencies: []
priority: medium
ordinal: 83000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A non-loopback receiver bind with no token/secret starts and 403s everything - a warning today, while comparably broken configs (pprof without admin) hard-fail in Validate() (internal/config/validate.go). Non-functional deserves the hard-fail treatment insecure gets. Escalate to a validation error with a documented override if someone genuinely wants auth-off on loopback-adjacent networks.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The configuration fails validation with an actionable message
- [ ] #2 Intentional configurations retain an explicit, documented path
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Keep validate.go behaviour as-is — no new error path.
2. Emit a self-obs gauge for "receiver bound non-loopback with no token/secret", per receiver, catalogued in internal/appcatalog.
3. Add the status-page row alongside the existing config-health surface.
4. Give the new signal a real dashboard panel (the coverage gate has no escape hatch) — return the panel spec to the dashboard-regroup owner rather than editing tabs/ directly.
5. Sharpen the existing Warnings() text so it states the consequence (everything 403s) rather than only the condition.
6. File the deferred hard-fail as its own task tagged for the next major.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
OWNER DECISION 2026-08-30: NOT breaking this wave. Do NOT escalate to a Validate() error. A hard fail refuses configs that boot today, which cuts 5.0.0 and forces the Go module path to /v5 before the release PR; the owner declined that for this wave.

Ship instead: keep the startup warning, make it loud, and give the misconfiguration a self-obs metric plus a status-page row so it is visible and alertable rather than only present in a log line nobody re-reads. Re-file the hard-fail against a future major.

Repo state for context: released 4.0.1, currently accumulating on v4.1.0-rc.29. Nothing else in the remaining board is breaking, so this wave stays entirely on 4.x.
<!-- SECTION:NOTES:END -->
