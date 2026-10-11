---
id: TSO-0189
title: Keep device OS pie-chart labels readable at panel snapshot dimensions
status: Parked
assignee: []
created_date: '2026-10-11 17:15'
updated_date: '2026-10-11 18:22'
labels:
  - needs-triage
dependencies: []
priority: low
type: bug
ordinal: 189000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
During TSO-0172 (live dashboard audit), an independently inspected published Devices by OS panel at 800x600 rendered a populated pie with a readable right-side legend, but long in-chart labels overlapped and one clipped at the left boundary; small-slice percentage annotations were partly obscured. This is a layout readability issue, not a query error. No lab labels or values are needed to reproduce it. Filed during loop28; not admitted to its frozen envelope.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A synthetic multi-category fixture with long OS labels and small slices keeps every category readable at 800x600 and normal dashboard width.
- [ ] #2 The visualization makes the percentage-versus-count interpretation explicit without clipped or overlapping annotations.
- [ ] #3 Retain the existing query and signal coverage and capture before/after rendered fixture evidence.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.
<!-- SECTION:NOTES:END -->
