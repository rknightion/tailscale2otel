---
id: TSO-0195
title: Harden fleet-summary listing prerequisite regression guard
status: Done
assignee:
  - '@loop31'
created_date: '2026-10-12 21:33'
updated_date: '2026-10-13 09:55'
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
- [x] #1 A deliberate removal of the listing-specific successful-state filter fails the durable regression check for the intended reason.
- [x] #2 Correct failed-listing, empty-successful-listing and populated selected-scope behavior passes without altering dashboard semantics or weakening existing tests.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: implement only the frozen owner-approved seam in isolated ownership; retain exact candidate and behavior evidence, named gate where applicable; independent guarded review then root integration/CI/delivery. Read-only diagnosis has no remediation or code gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop31: 1 implementation attempt; strengthened listing-specific success guard and executed nine-scenario real-query fixtures, intended deletion fails. Candidate gate green after one parallel-linter infrastructure retry, full-hunk guarded review PASS. Sharded CodeRabbit staged-Python No files to review defect is explicitly unreviewed under approved review disposition, not clean. Landed0659d26129a583974443ff989c9c02184ba216fb; fresh exact composed gate green; exact CI38377130103 ci-success, sync38377130106 and downstream automation succeed. Both far-side dashboard blobs equal artifacts at published SHA. Helm absent by push paths. Minor future fixture-input drift risk left: changed fixture inputs require fresh executed receipts; current inputs/positive/negative proof verified. No dashboard query semantics changed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Bound the fleet zero fallback guard to the listing-specific successful supported-state operand, preserving online and other existing assertions. Durable negative mutation and actual failed/empty/populated/scope fixtures verified; integrated/published with exact composed and CI evidence. Attempts1; complete.
<!-- SECTION:FINAL_SUMMARY:END -->
