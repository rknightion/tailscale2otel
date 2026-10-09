---
id: TSO-0161
title: >-
  Classify a malformed HTTP status line as class other, never by its free-text
  word
status: In Progress
assignee:
  - '@loop20'
created_date: '2026-10-09 22:26'
updated_date: '2026-10-09 22:32'
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
- [ ] #1 An exporter error carrying net/http's malformed HTTP status or response text is classified as other with an empty reason, whatever word follows the version
- [ ] #2 Genuine transport timeouts (i/o timeout from a dial or read, context deadline exceeded) still classify as timeout
- [ ] #3 A new table case in internal/telemetry/delivery_diag_test.go fails before the change and passes after; go test -run TestClassifyExportError ./internal/telemetry/ passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement frozen scope; regression red then green; exact candidate gate and independent review before landing.
<!-- SECTION:PLAN:END -->
