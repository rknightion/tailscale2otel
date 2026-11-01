---
id: TSO-0226
title: Keep rolling audit-log counts distinct from per-second rates
status: To Do
assignee: []
created_date: '2026-11-01T22:55:00Z'
labels:
  - follow-up
  - dashboards
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 228000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Several Audit Trail and Log explorer panels label count_over_time over a variable automatic interval with count-per-second units. The query returns window counts; changing the automatic interval changes the quantity while the displayed rate interpretation remains fixed. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Affected panel units and titles distinguish event counts over a window from true per-second rates.
- [ ] #2 A constant-rate fixture evaluated at two different auto intervals preserves the intended interpretation.
- [ ] #3 All existing action, target, event, tailnet and provider grouping/filter semantics remain intact.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
