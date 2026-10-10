---
id: TSO-0161
title: >-
  Classify a malformed HTTP status line as class other, never by its free-text
  word
status: Done
assignee:
  - '@loop20'
created_date: '2026-10-09 22:26'
updated_date: '2026-10-10 00:23'
labels: []
dependencies: []
references:
  - internal/telemetry/delivery.go
priority: medium
type: bug
ordinal: 161000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TSO-0153 residual (Low, accepted in loop19): classifyExportError in internal/telemetry/delivery.go falls through to its keyword switch for a malformed status line such as 'HTTP/1.1 timeout', so a backend-chosen word picks the delivery-failure class and the allowlisted reason. The output is still from the fixed set and leaks nothing, but the backend steers classification. Decided 2026-10-09: malformed HTTP maps to class other with an empty reason; no new reason constant. A genuine transport timeout (dial or read 'i/o timeout', context deadline) must keep class timeout.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An exporter error carrying net/http's malformed HTTP status or response text is classified as other with an empty reason, whatever word follows the version
- [x] #2 Genuine transport timeouts (i/o timeout from a dial or read, context deadline exceeded) still classify as timeout
- [x] #3 A new table case in internal/telemetry/delivery_diag_test.go fails before the change and passes after; go test -run TestClassifyExportError ./internal/telemetry/ passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement frozen scope; regression red then green; exact candidate gate and independent review before landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop20: 2 implementation attempts; Done after acceptance, landed code and green composed gate. Earlier superseded/cancelled CI runs are not passes.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
loop20 landed evidence 07acc0e93b2d7dee6dca290da2da21f7fe9726da. Malformed HTTP status, response and version shapes classify as other with empty reason while genuine transport timeouts retain timeout. Real metric/log wire reproductions observed red then green. Independent original/delta reviews PASS; CodeRabbit minor version omission fixed. Final composed just check on actual shipped SHA129945fb3230f1c5d8a1127ad3e3c7faa2c514f4 passed, unchanged tree. Exact CI e96e74e31e2df4c697435dd4b7a32315e0e02f41 run38003933710 ci-success succeeded; no production diff outside backlog from that tested code. Helm absent-not-required (no landed chart change). Just formatting passed. Generated-input DoD is not applicable or covered by gate; no generator drift or weakened test claimed.
<!-- SECTION:FINAL_SUMMARY:END -->
