---
id: TSO-0153
title: Sanitize exporter first-failure diagnostics before logging backend responses
status: Parked
assignee: []
created_date: '2026-10-08 22:51'
updated_date: '2026-10-09 13:36'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 153000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
An adjacent security review found internal/telemetry/delivery.go logs the raw first-failure error. OTLP SDK non-2xx errors can include backend response text, which may contain sensitive diagnostic material. This pre-existing behavior is separate from the ingress WAL cadence and HTTP acknowledgement correction; no credential or customer instance is included in this finding.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 First-failure and outage diagnostics preserve useful bounded error classes without logging arbitrary backend response bodies or credential-bearing content
- [ ] #2 Local fake HTTP exporter response with a synthetic secret sentinel proves logs exclude the sentinel while failure classification remains visible
- [ ] #3 Unknown joined transport errors and recovery summaries retain correct health accounting without exposing raw response text
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop18: 0 implementation attempts; dependency park. D3 serial follow-on cannot start because the WAL build/harness remain unfinished, and delivery.go is part of the retained build candidate. Resume only after the build and identical-entry harness are accepted and landed.
<!-- SECTION:NOTES:END -->
