---
id: TSO-0157
title: >-
  Report CodeRabbit finding counts in the sharded review summary instead of
  CLEAN
status: Done
assignee:
  - '@loop19'
created_date: '2026-10-09 17:29'
updated_date: '2026-10-09 17:55'
labels:
  - tooling
  - coderabbit
dependencies: []
priority: medium
type: bug
ordinal: 157000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
scripts/shard_coderabbit_review.py prints '[dir] CLEAN (complete)' for every shard that exits 0 with a complete line, regardless of findings. On the loop19 TSO-0148 review it printed '7 clean, 0 failed' while the aggregate NDJSON held 4 major and 2 minor findings. An operator or agent reading the summary takes a false pass. The summary must report per-shard finding counts by severity, and 'clean' must mean zero findings.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Per-shard status lines and the final summary report finding counts by severity from the shard's NDJSON; a shard with findings is never labelled clean
- [x] #2 A completed shard with findings still exits 0 (findings are not transport failures), and incomplete/failed shards keep failing closed as today
- [x] #3 scripts/test_shard_coderabbit_review.py covers a completed shard with major findings and shows the old summary failing that test
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop19: loop-created from the TSO-0148 CodeRabbit run; 1 implementation attempt; landed 446f0682; reviewer approve; CodeRabbit 0 findings; composed just check green; ci-success green. Advisory left: cross-check complete.findings against counted finding events.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Sharded CodeRabbit runner now prints per-shard finding counts by severity (CLEAN only at zero findings) and totals in the summary; exit semantics unchanged. Landed 446f0682.
<!-- SECTION:FINAL_SUMMARY:END -->
