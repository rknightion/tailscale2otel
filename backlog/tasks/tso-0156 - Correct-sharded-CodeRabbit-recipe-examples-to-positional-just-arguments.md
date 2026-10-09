---
id: TSO-0156
title: Correct sharded CodeRabbit recipe examples to positional just arguments
status: To Do
assignee: []
created_date: '2026-10-09 13:00'
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
- [ ] #1 Examples pass the base and directory list as positional recipe arguments, matching just --show review-sharded
- [ ] #2 A dry-run of the documented example produces the intended --base and --dirs arguments
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
