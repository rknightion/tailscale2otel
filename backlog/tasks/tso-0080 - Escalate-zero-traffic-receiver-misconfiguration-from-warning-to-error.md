---
id: TSO-0080
title: Surface zero-traffic receiver misconfiguration without breaking startup
status: In Progress
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-31 00:29'
labels: []
milestone: m-6
dependencies: []
priority: medium
ordinal: 83000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A non-loopback receiver bind with no token or secret starts successfully but refuses traffic with HTTP 403. Preserve that existing startup compatibility in Wave 3 while making the condition operationally loud through an actionable warning, a per-receiver self-observability metric, and a status-page row. Track a Validate() hard-fail separately for a future major release.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The non-loopback receiver-without-credential condition remains non-breaking but emits an actionable warning, a per-receiver self-observability metric, and a status-page row
- [ ] #2 The deferred hard-fail is tracked for a future major release
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

Owner correction applied: preserve Validate() compatibility, sharpen the warning, add per-receiver self-observability and status visibility, then file the hard-fail for a future major.

Dashboard ownership timing: F2 lands before this lane. After F2, the implementing lane edits the tab module assigned by TSO-0092's ownership map directly and regenerates dashboards; only a missing shared helper or layout seam returns to lane L.

Lane E keeps startup non-breaking, sharpens the warning, and returns the receiver misconfiguration descriptor/status wiring for root-owned seams while adding its assigned-tab panel.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
OWNER DECISION 2026-08-30: NOT breaking this wave. Do NOT escalate to a Validate() error. A hard fail refuses configs that boot today, which cuts 5.0.0 and forces the Go module path to /v5 before the release PR; the owner declined that for this wave.

Ship instead: keep the startup warning, make it loud, and give the misconfiguration a self-obs metric plus a status-page row so it is visible and alertable rather than only present in a log line nobody re-reads. Re-file the hard-fail against a future major.

Repo state for context: released 4.0.1, currently accumulating on v4.1.0-rc.29. Nothing else in the remaining board is breaking, so this wave stays entirely on 4.x.

Wave 3 root resolved the stale task contract in favour of the frozen no-breaking-change decision; the original validation-error acceptance criteria were replaced through the Backlog CLI.

Review clarification: the title and description now match the owner's frozen non-breaking Wave 3 remedy. The future hard fail remains follow-up work for a later major.

CodeRabbit's panel-routing clarification was accepted. The metric-temporality implementation finding belongs to TSO-0068's post-F2 lane; F1 intentionally freezes behavior-preserving config before wiring.
<!-- SECTION:NOTES:END -->
