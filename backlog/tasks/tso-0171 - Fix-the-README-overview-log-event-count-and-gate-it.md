---
id: TSO-0171
title: Fix the README overview log-event count and gate it
status: In Progress
assignee:
  - '@loop24'
created_date: '2026-10-10 21:46'
updated_date: '2026-10-10 23:11'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 171000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The README overview table says 30 log-event types while internal/catalog/capability_counts.json, docs/index.md and docs/comparison.md say 31. CodeRabbit flagged it during loop23 but it was outside that loop's ownership. scripts/check-capability-counts.py checks the README sentence "All N metrics and M log events" but has no pattern for the overview table row (`**334** metrics + **30** log-event types | across **17** collectors`), which is how the figure drifted without a red gate.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 scripts/check-capability-counts.py checks the README overview table row figures (metrics, log-event types, collectors) against internal/catalog/capability_counts.json, and is seen failing on the current README before the prose fix
- [ ] #2 README.md overview table states the log-event count from capability_counts.json and the check passes; no other count prose changes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add the missing overview-row count pattern, observe failure against current README, correct only the row, then staged full gate and independent plus CodeRabbit review before root landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop24: 1 implementation attempt; negative checker proof fails old 30 and passes corrected 31. Full staged local gate exit 0, just formatter exit 0; CodeRabbit complete with zero findings covering both owned files, independent reviewer PASS on tree 43959243ae21036b4873a2d0188936f795d11a26. Root landing then exact-SHA composed gate and CI required before Done. Evidence /Users/rob/repos/tailscale2otel/codex/loop24/evidence-0171/ and review /Users/rob/repos/tailscale2otel/codex/loop24/review-0171.md.
<!-- SECTION:NOTES:END -->
