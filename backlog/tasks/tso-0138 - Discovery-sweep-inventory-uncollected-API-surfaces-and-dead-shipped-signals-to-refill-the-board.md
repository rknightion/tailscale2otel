---
id: TSO-0138
title: >-
  Discovery sweep: inventory uncollected API surfaces and dead shipped signals
  to refill the board
status: To Do
assignee: []
created_date: '2026-09-05 17:16'
labels: []
dependencies: []
references:
  - spec/tailscale-api.json
  - spec/changelog-reviewed.json
  - internal/tsapi/contract/field_dispositions.json
priority: medium
type: spike
ordinal: 139000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The board drained on 2026-09-04: two tasks remain and one is date-gated. The former candidates file was drained into TSO-0033 to TSO-0086 on 2026-08-30 and deleted, so there is no standing candidate list to draw the next wave from. Sources to sweep, each with a consumer to check against: the 35 GET operations in spec/tailscale-api.json against internal/tsapi and the collectors; the Border0 endpoints in the PAM API reference doc section 2 against internal/b0api; that doc's section 9 open questions; the audit-event enum in the vendored spec against the classifier categories; the parked verdicts in spec/changelog-reviewed.json; and the shipped metric and log families against what has actually received samples on the stack over the trailing 30 days, because a dead shipped signal is as much a finding as a missing one. The output is tasks, not code: every adopted candidate becomes a To Do task labelled needs-triage for the owner to accept or reject, and every rejected candidate is recorded here with its reason so it is not re-proposed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A ledger with a fixed schema (source, surface, consumed-by or none, evidence, proposed disposition) covers every GET operation in the vendored spec and every Border0 endpoint in the PAM reference doc section 2, with no row left blank
- [ ] #2 Every shipped metric and log family is checked for samples on the stack over the trailing 30 days through read-only queries, and each zero-sample family is listed with a proposed disposition: dead signal, lab-shape gap, or expected-quiet
- [ ] #3 Each candidate the root adopts is filed as a To Do task labelled needs-triage with a description stating the need and testable acceptance criteria; each rejected candidate is recorded on this task with its reason
- [ ] #4 No candidate re-proposes a surface that already carries a rejected or adopted verdict in spec/changelog-reviewed.json, or a Done task, without new evidence stated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
