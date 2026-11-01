---
id: TSO-0233
title: Render duration rates as dimensionless quantities
status: To Do
assignee: []
created_date: '2026-11-01T23:19:00Z'
labels: []
dependencies: []
priority: medium
ordinal: 235000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The published Time waiting vs time on the wire panel expresses seconds spent per second but formats its axis as seconds/milliseconds. Log catch-up skipped seconds/s has the same duration-rate unit mismatch. A rate of duration is dimensionless; readers must not interpret the displayed value as latency. Filed by TSO-0172 (comprehensive live dashboard audit), outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both affected duration-rate panels use a dimensionless or clearly labelled seconds-per-second unit rather than a duration unit.
- [ ] #2 A deterministic nonzero fixture distinguishes elapsed seconds from seconds per second in both panels.
- [ ] #3 Retained normal-placement renders show accurate units and readable legends for both panels.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
