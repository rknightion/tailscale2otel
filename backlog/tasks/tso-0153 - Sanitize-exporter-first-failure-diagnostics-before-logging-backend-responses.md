---
id: TSO-0153
title: Sanitize exporter first-failure diagnostics before logging backend responses
status: Done
assignee:
  - '@loop19'
created_date: '2026-10-08 22:51'
updated_date: '2026-10-09 19:31'
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
- [x] #1 First-failure and outage diagnostics preserve useful bounded error classes without logging arbitrary backend response bodies or credential-bearing content
- [x] #2 Local fake HTTP exporter response with a synthetic secret sentinel proves logs exclude the sentinel while failure classification remains visible
- [x] #3 Unknown joined transport errors and recovery summaries retain correct health accounting without exposing raw response text
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop18: 0 implementation attempts; dependency park. D3 serial follow-on cannot start because the WAL build/harness remain unfinished, and delivery.go is part of the retained build candidate. Resume only after the build and identical-entry harness are accepted and landed.

loop19: 1 implementation attempt + 1 review-repair round; landed 303616b9; security review approve (residual Low: one-word malformed HTTP status line can pick a class from the fixed list, no leak); CodeRabbit 0 findings; composed just check green; CI green.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Delivery-health lines log only signal, bounded error class and allowlisted reason; classification uses numeric HTTP status / gRPC code, never server text; non-2xx 'partial success' counts as failure. Landed 303616b9.
<!-- SECTION:FINAL_SUMMARY:END -->
