---
id: TSO-0133
title: 'Review the coordination alert firing history and decide paging, due 2026-09-11'
status: To Do
assignee: []
created_date: '2026-09-04 07:31'
labels: []
dependencies: []
priority: low
type: chore
ordinal: 134000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The three coordination rules shipped 2026-09-04 as advisory and non-paging on purpose: nothing had watched them evaluate. The owner chose a cadence rather than a prediction. On or after 2026-09-11, after seven days of live evaluation, bring him the per-rule firing history and he decides which of CoordinationNoLeader, CoordinationSplitBrain and CoordinationNoStandby acquire paging labels.

A cadence cannot be wrong about the future the way a guess can, which is why this is a dated task rather than a threshold written into the rules now.

Bring, per rule: how many times it fired, how long each firing lasted, and whether each firing corresponded to something real. CoordinationNoStandby in particular is expected to fire permanently against any single-replica coordinated deployment, so separate that from genuine firings before presenting the history.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Per-rule firing history since 2026-09-04 is gathered from the stack and presented with each firing classified as real or spurious
- [ ] #2 The owner's per-rule paging decision is recorded, and any rule he promotes gets its label change shipped and proved by a real gcx resources push
- [ ] #3 If a rule proved untrustworthy it is fixed or withdrawn rather than left evaluating and ignored
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
