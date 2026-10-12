---
id: TSO-0191
title: Land the dashboard selector-scope guard from the retained audit candidate
status: To Do
assignee: []
created_date: '2026-10-12 19:03'
labels: []
dependencies: []
references:
  - deploy/grafana/gen
  - reference/grafana-delivery.md
priority: high
type: bug
ordinal: 191000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Split out of TSO-0172 (comprehensive dashboard audit) after loop28 and loop29 landed nothing: the audit's static, locally provable half was held hostage to its live-evidence half. TSO-0170 found a whole tab ignoring the tailnet and provider variables; the loop28 audit lane built a generated-dashboard selector-scope guard plus the generator fixes and regenerated both dashboards, with the full gate green and an independent security review passed, but it was never landed. The retained staged candidate lives in the gitignored codex/loop28 campaign directory (selector_scope.py, test_selector_scope.py, build.py, two existing test expectation lines and both generated dashboard JSONs) on a base that main has since moved past with dependency bumps only. Live per-panel verdicts and query evidence stay in TSO-0172.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every metric, log and trace query on every tab of both generated dashboards honours every dashboard variable that applies to it (tailnet, provider and any tab-local selector), enforced by a generated-dashboard test across all tabs, not only Policy and Config
- [ ] #2 That test is seen failing on a deliberately unscoped query before it passes, and the failing output is recorded in the task notes
- [ ] #3 Existing deploy/grafana/gen test expectation strings change only by adding the tailnet matcher; every other existing assertion is unchanged
- [ ] #4 Both generated dashboard JSONs are regenerated from the generator with no hand edits, and the landed commit's ci-success is green with grafana-sync delivering both dashboards
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
