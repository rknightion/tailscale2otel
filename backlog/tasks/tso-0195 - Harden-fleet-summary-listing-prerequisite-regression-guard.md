---
id: TSO-0195
title: Harden fleet-summary listing prerequisite regression guard
status: In Progress
assignee:
  - '@loop31'
created_date: '2026-10-12 21:33'
updated_date: '2026-10-13 08:54'
labels:
  - needs-triage
dependencies: []
priority: low
type: task
ordinal: 195000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Fresh review found the current fleet-summary query is correct and real failed-listing fixtures pass, but a permanent test only looks for == 1 anywhere in the expression. The fleet-online predicate already contains that text, so removal of the listing-specific supported-state filter could evade the portable assertion and manufacture a reassuring zero for failed listing. Retained behavioral fixtures catch it locally; durability in the regular test boundary is the follow-up, not a current display defect.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A deliberate removal of the listing-specific successful-state filter fails the durable regression check for the intended reason.
- [ ] #2 Correct failed-listing, empty-successful-listing and populated selected-scope behavior passes without altering dashboard semantics or weakening existing tests.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: implement only the frozen owner-approved seam in isolated ownership; retain exact candidate and behavior evidence, named gate where applicable; independent guarded review then root integration/CI/delivery. Read-only diagnosis has no remediation or code gate.
<!-- SECTION:PLAN:END -->
