---
id: TSO-0234
title: Label histogram heatmap intensity as observation frequency
status: To Do
assignee: []
created_date: '2026-11-01T23:19:00Z'
labels: []
dependencies: []
priority: medium
ordinal: 236000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The published Scrape duration distribution heatmap has duration buckets on its vertical axis, but its color intensity legend is also formatted as duration. Bucket frequency and bucket boundary are different quantities. Filed by TSO-0172 (comprehensive live dashboard audit), outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Duration buckets retain their duration unit while color intensity describes bucket count or count rate consistent with the query.
- [ ] #2 A deterministic multi-bucket fixture proves that intensity tracks observation frequency rather than duration.
- [ ] #3 A retained normal-placement render shows both bucket and intensity meanings without ambiguous units.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
