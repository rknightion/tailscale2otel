---
id: TSO-0083
title: Headscale and multi-tailnet starter configs in examples/config
status: Done
assignee: []
created_date: '2026-08-30 09:36'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-7
dependencies:
  - TSO-0077
priority: medium
ordinal: 86000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
examples/config/ has only grafana-cloud, prometheus and stdout starters despite Headscale support and multi-tailnet/MSP mode being headline README features - real onboarding friction for two first-class paths. Add a Headscale starter and a multi-tailnet starter, validated by configcheck like the existing examples, and link them from the README/docs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Both starters exist, pass configcheck, and are referenced from the docs
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane K adds and configcheck-validates Headscale and multi-tailnet starters, then links them from existing onboarding docs.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane K added configcheck-validated Headscale and multi-tailnet starter configs and linked them from README/getting-started. Focused starter/configcheck tests passed.

Required CodeRabbit pre-commit review attempted on the integrated staged diff after just check passed; the service failed before analysis with recoverable  and emitted no  line. Treated as a failed review, not a clean result. Root manually reviewed the full staged diff and found no blocking issue; this is an overnight review-service deviation.

Correction to the preceding note: the exact recoverable error was WebSocket closed, and the review emitted no complete status line.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added Headscale and multi-tailnet starter configs, linked them from onboarding docs, and made supported starters part of configcheck and focused load validation. Implementation SHA d3af40f. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
