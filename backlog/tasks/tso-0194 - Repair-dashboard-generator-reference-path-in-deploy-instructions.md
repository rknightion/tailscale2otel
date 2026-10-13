---
id: TSO-0194
title: Repair dashboard-generator reference path in deploy instructions
status: In Progress
assignee:
  - '@loop31'
created_date: '2026-10-12 20:54'
updated_date: '2026-10-13 09:17'
labels:
  - needs-triage
dependencies: []
priority: low
type: docs
ordinal: 194000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Several dashboard lanes and reviewers found the deploy instructions point to a repository-root reference/dashboard-generator document that is absent; the reference actually lives under deploy/reference. The missing discovery link forced bounded rediscovery during guarded work. Source/gate behavior was unaffected, and the instruction repair was outside the frozen implementation packets.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every deploy instruction link for the dashboard generator resolves to its actual maintained reference.
- [ ] #2 Validate the affected documentation links without changing generator, gate or workflow behavior.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: correct only deploy instruction generator-reference path, run targeted link resolution and named gate; root guarded documentation review and integration. No generator/gate/workflow changes.
<!-- SECTION:PLAN:END -->
