---
id: TSO-0177
title: Make the documented API spec refresh failure-atomic
status: Done
assignee:
  - loop27
created_date: '2026-10-11 12:05'
updated_date: '2026-10-11 15:57'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/spec/README.md
priority: low
type: docs
ordinal: 177000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
CodeRabbit found that the new YAML-to-JSON refresh example redirects yq output directly into the vendored JSON baseline. A conversion failure can truncate a previously valid baseline. The completed vendor refresh itself is byte-identical to the successful upstream conversion; this is a future operator-command safety issue, not a schema defect.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A failed download or YAML conversion leaves the existing vendored JSON byte-identical.
- [x] #2 A successful refresh replaces the baseline with valid JSON matching the conversion and cleans up temporary files; the example is exercised on local success/failure fixtures.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop27: isolated staged documentation candidate with local success/failure fixtures; root review, landing and exact-SHA gates.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 1 implementation attempt; two no-charge infrastructure retries for unrelated golangci-lint lock contention. Independent reviewer OK, CodeRabbit skipped docs. Landed b9544f82b1b479d96c3064703e48a3eff31c8257; exact composed setup + just check exit0 (561.888s, 71 passed 0 failed, clean tree). Required ci-success success in run38152648367. No generated inputs changed; generation DoD not applicable.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Refresh now downloads/converts in temporary files and atomically renames successful JSON. Old snippet negative fixture corrupts baseline; fixed download/conversion failures preserve bytes and success matches conversion with no temps. Source SHA b9544f82b1b479d96c3064703e48a3eff31c8257, exact composed gate and ci-success run38152648367 green. helm-success absent, not a pass.
<!-- SECTION:FINAL_SUMMARY:END -->
