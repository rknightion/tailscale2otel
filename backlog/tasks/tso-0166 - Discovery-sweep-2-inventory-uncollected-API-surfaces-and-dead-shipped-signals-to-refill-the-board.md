---
id: TSO-0166
title: >-
  Discovery sweep 2: inventory uncollected API surfaces and dead shipped signals
  to refill the board
status: To Do
assignee: []
created_date: '2026-10-09 22:27'
labels: []
dependencies: []
references:
  - spec/tailscale-api.json
  - spec/changelog-reviewed.json
priority: medium
type: spike
ordinal: 166000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The board drained again on 2026-10-09 when loop19 closed nothing-admissible. TSO-0138 (the previous discovery sweep, 2026-09-05) is the template; its notes record what was adopted and rejected. Sources to sweep: the GET operations in spec/tailscale-api.json against internal/tsapi and the collectors; the Border0 endpoints in the PAM API reference against internal/b0api; the audit-event enum against the classifier; the parked verdicts in spec/changelog-reviewed.json; and every shipped metric and log family against samples on the lab Grafana stack over the trailing 30 days, using read-only queries only. The ledger lives in a gitignored path, because the repository is public and stack captures stay out of it. The output is tasks, not code.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A ledger with a fixed schema (source, surface, consumed-by or none, evidence, proposed disposition) covers every GET operation in the vendored spec and every Border0 endpoint, with no blank rows, under the gitignored codex/ tree
- [ ] #2 Every shipped metric and log family is checked for samples over the trailing 30 days with read-only queries; each zero-sample family carries a proposed disposition (dead signal, lab-shape gap or expected-quiet), and any change since TSO-0138's 116 absences is called out
- [ ] #3 Each adopted candidate is filed as a To Do task labelled needs-triage, with a need statement and testable acceptance criteria; each rejected candidate is recorded on this task with its reason
- [ ] #4 No candidate re-proposes a surface with an existing verdict in spec/changelog-reviewed.json, TSO-0138's notes or a Done task without stating new evidence
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
