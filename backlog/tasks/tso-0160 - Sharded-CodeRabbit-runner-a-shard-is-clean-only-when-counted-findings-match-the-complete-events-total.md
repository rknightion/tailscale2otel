---
id: TSO-0160
title: >-
  Sharded CodeRabbit runner: a shard is clean only when counted findings match
  the complete event's total
status: In Progress
assignee:
  - '@loop20'
created_date: '2026-10-09 22:26'
updated_date: '2026-10-09 22:32'
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
- [ ] #1 A shard whose counted finding events differ from its complete event's findings field is reported not-clean with a distinct mismatch reason, and the run exits non-zero
- [ ] #2 A complete event with a missing or non-integer findings field is reported not-clean
- [ ] #3 A new unittest in scripts/test_shard_coderabbit_review.py fails against the pre-change script and passes after; python3 -m unittest scripts.test_shard_coderabbit_review passes
- [ ] #4 docs/coderabbit-sharded-review.md states the mismatch rule
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
