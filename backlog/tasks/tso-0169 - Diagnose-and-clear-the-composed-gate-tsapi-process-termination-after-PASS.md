---
id: TSO-0169
title: Diagnose and clear the composed gate tsapi process termination after PASS
status: Parked
assignee:
  - '@loop20'
created_date: '2026-10-09 23:06'
updated_date: '2026-10-09 23:13'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 169000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Loop20 composed just check on integrated SHA 07acc0e93b2d7dee6dca290da2da21f7fe9726da exited 1. The internal/tsapi test process printed PASS then signal: terminated after 24.903 seconds with no failing assertion. fmt, lint and vet passed; all remaining gate legs did not run. An unchanged rerun alone cannot explain this; identify termination cause and repair within scope or demonstrate a bounded infrastructure retry cause.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The signal termination is attributed with concrete process/test evidence rather than inferred from an eventual green retry
- [ ] #2 Any code or fixture repair preserves test strength and is observed failing then passing; infrastructure-only resolution records exact cause and retry identity
- [ ] #3 The composed just check passes on the final integrated SHA with every required leg run
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read composed process/log evidence, classify the post-PASS termination, freeze a narrow repair only if a defect is proven; no blind unchanged gate retry.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Read-only diagnosis on 07acc0e93b2d7dee6dca290da2da21f7fe9726da: process SIGTERM after PASS, sender unknown. Gate and wrapper did not timeout; no recorded test-driven or harness kill. Host AMFI/EXC_GUARD observations also accompany passing test binaries and are not proved causal. Bounded 268-case targeted race/json test passed unchanged, which is not a repair or composed acceptance. Original signal provenance was not captured. Root prohibits blind unchanged composed retry; next full composed gate must cover a genuinely changed integrated SHA. Historical attribution remains unproven.

loop20: 0 implementation attempts (read-only diagnosis). Parked evidence/instrumentation defect: original SIGTERM sender absent from historical receipts, so attribution AC cannot honestly be checked. No source defect or safe code repair identified. Resume with original signal provenance or a captured recurrence including PID/PPID/process group and sender/cancellation evidence; do not swallow SIGTERM or weaken gate. New integrated-SHA gate remains separately required for landed batch acceptance.
<!-- SECTION:NOTES:END -->
