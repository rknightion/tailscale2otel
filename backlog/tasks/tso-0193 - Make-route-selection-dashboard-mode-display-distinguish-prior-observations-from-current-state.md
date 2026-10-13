---
id: TSO-0193
title: >-
  Make route-selection dashboard mode display distinguish prior observations
  from current state
status: Done
assignee:
  - '@loop31'
created_date: '2026-10-12 20:40'
updated_date: '2026-10-13 12:21'
labels:
  - needs-triage
dependencies: []
priority: low
type: bug
ordinal: 193000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The route-selection info gauge clears obsolete SDK series after a successful poll, but its generated table uses the established two-hour configuration evidence window. A mode change can leave prior and current stored modes visible together, and loss of mode support can show an earlier observation. This is a display limitation, not an alert or control-plane change. Fresh review established it from the generated expression; no live instance values are required.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The route-mode display either selects the latest applicable observation or explicitly distinguishes prior observations from current state.
- [x] #2 Actual query fixtures cover a mode change and absent current mode without erasing useful history or selected tailnet/provider scope.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: retain scoped two-hour query/history/title, label observed route modes explicitly historical; reproduce actual mode-change/absent-current fixtures, portable tests and named gate, then root guarded review/integration/CI/delivery.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop31: 1 implementation attempt; historical two-hour observation copy with unchanged scoped query/title/history and predecessor OS presentation. Actual mode-change/prior-only/stale/expiry/tailnet/provider fixtures pass, portable tests negative before correction; full-hunk guarded review PASS. Sharded CodeRabbit Python No files defect explicitly unreviewed, not clean. Candidate and exact fresh composed gate pass; composed needed one parallel-linter infrastructure retry, no source correction. Published2480a1f298ac86044d87362566bc035aae6caf4f CI38390571999 ci-success and sync38390572002 plus downstream publication success. Exact far-side tailnet022a15fdaddf058c6bdc157c67cec9b75ef17077 matches. Helm absent by paths. Browser/live query backend not exercised for this copy-only change; actual local generated PromQL fixtures were executed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Route-selection display now explicitly describes historical observations, including prior/current coexistence and prior-only support loss, without changing query/history/title/scope. Actual query fixtures, guarded review, composed gate, CI and GitSync verified. Attempts1; complete.
<!-- SECTION:FINAL_SUMMARY:END -->
