---
id: TSO-0160
title: >-
  Sharded CodeRabbit runner: a shard is clean only when counted findings match
  the complete event's total
status: Done
assignee:
  - '@loop20'
created_date: '2026-10-09 22:26'
updated_date: '2026-10-10 00:23'
labels: []
dependencies: []
references:
  - scripts/shard_coderabbit_review.py
  - docs/coderabbit-sharded-review.md
priority: medium
type: chore
ordinal: 160000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TSO-0157 made scripts/shard_coderabbit_review.py report per-shard finding counts from 'finding' events, but it never reads the 'findings' field on the shard's 'complete' event. If the CLI drops or truncates finding events, the shard still reports clean. Loop19's TSO-0157 review left this as an advisory; loop20 preparation (2026-10-09) adopted it. Decided: a complete event with no findings field, or a non-integer one, is not-clean (fail closed); a mismatch is a failure with its own reason string and a non-zero exit.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A shard whose counted finding events differ from its complete event's findings field is reported not-clean with a distinct mismatch reason, and the run exits non-zero
- [x] #2 A complete event with a missing or non-integer findings field is reported not-clean
- [x] #3 A new unittest in scripts/test_shard_coderabbit_review.py fails against the pre-change script and passes after; python3 -m unittest scripts.test_shard_coderabbit_review passes
- [x] #4 docs/coderabbit-sharded-review.md states the mismatch rule
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
loop20: 1 implementation attempts; Done after acceptance, landed code and green composed gate. Earlier superseded/cancelled CI runs are not passes.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
loop20 landed evidence 50cb41a27f72e973b362dfee460b00514bf91bba. Counted findings must match every integer completion total; missing/noninteger totals fail closed. CLI subprocess tests observed red then green; independent review PASS; completed CodeRabbit reviewed both scripts with zero findings. Final composed just check on actual shipped SHA129945fb3230f1c5d8a1127ad3e3c7faa2c514f4 passed, unchanged tree. Exact CI e96e74e31e2df4c697435dd4b7a32315e0e02f41 run38003933710 ci-success succeeded; no production diff outside backlog from that tested code. Helm absent-not-required (no landed chart change). Just formatting passed. Generated-input DoD is not applicable or covered by gate; no generator drift or weakened test claimed.
<!-- SECTION:FINAL_SUMMARY:END -->
