---
id: TSO-0194
title: Repair dashboard-generator reference path in deploy instructions
status: Done
assignee:
  - '@loop31'
created_date: '2026-10-12 20:54'
updated_date: '2026-10-13 13:01'
labels:
  - needs-triage
dependencies: []
priority: low
type: docs
ordinal: 194000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Several dashboard lanes and reviewers found the deploy instructions point to a repository-root reference/dashboard-generator document that is absent; the reference actually lives under deploy/reference. The missing discovery link forced bounded rediscovery during guarded work. Source/gate behavior was unaffected, and the instruction repair was outside the frozen implementation packets.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every deploy instruction link for the dashboard generator resolves to its actual maintained reference.
- [x] #2 Validate the affected documentation links without changing generator, gate or workflow behavior.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: correct only deploy instruction generator-reference path, run targeted link resolution and named gate; root guarded documentation review and integration. No generator/gate/workflow changes.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop31: 1 implementation attempt; instruction generator reference resolves maintained repository-root-anchored target. Targeted negative/positive link scan, named candidate gate and guarded doc review PASS; CodeRabbit skipped documentation. Component051054ae26546b42b8f4c49385dc98ce89dcf1d7 included in published batch9927d3962b49aef5d13f3196c76f05cf722872b8 with exact composed gate and CI38383777876 ci-success green. No behavior change; generated inputs unchanged, no recipe change.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Corrected the one deploy-instruction generator reference; all affected links resolve with no generator/gate/workflow changes. Attempts1; complete.
<!-- SECTION:FINAL_SUMMARY:END -->
