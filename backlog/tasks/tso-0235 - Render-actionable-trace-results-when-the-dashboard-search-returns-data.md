---
id: TSO-0235
title: Render actionable trace results when the dashboard search returns data
status: To Do
assignee: []
created_date: '2026-11-01T23:20:00Z'
labels: []
dependencies: []
priority: medium
ordinal: 237000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The published Scrape to API trace waterfall panel shows No data found in response over the frozen three-hour audit window, while the exact source TraceQL search independently returns 20 trace results. A fresh published solo render reproduces the empty state without a visible query-error indicator. The query-to-visualization failure mechanism remains unresolved. Filed by TSO-0172 (comprehensive live dashboard audit), outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A deterministic populated trace-search fixture produces an actionable visible trace list or waterfall in the shipped panel rather than an empty-response message.
- [ ] #2 A genuine empty search response remains a truthful empty state, and query errors remain distinguishable.
- [ ] #3 Retained live source/query/render evidence uses one stated window and confirms usable results at normal panel placement.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
