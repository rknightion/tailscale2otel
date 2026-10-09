---
id: TSO-0156
title: Correct sharded CodeRabbit recipe examples to positional just arguments
status: Done
assignee:
  - '@loop19'
created_date: '2026-10-09 13:00'
updated_date: '2026-10-09 15:22'
labels: []
dependencies: []
priority: low
type: docs
ordinal: 156000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The documented just review-sharded base=main dirs=... invocation passes those strings literally into the positional recipe parameters. The runner then reports No Git repository found for the invalid dirs argument before any review is completed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Examples pass the base and directory list as positional recipe arguments, matching just --show review-sharded
- [x] #2 A dry-run of the documented example produces the intended --base and --dirs arguments
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop19: 1 implementation attempt; landed 645dc158 (positional review-sharded examples); reviewer PASS; composed just check green; ci-success green. Docs-only, CodeRabbit skipped.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Rewrote docs/coderabbit-sharded-review.md examples to the positional form (just review-sharded main "<dirs>"); dry-run prints --base 'main' --dirs ...; landed 645dc158.
<!-- SECTION:FINAL_SUMMARY:END -->
