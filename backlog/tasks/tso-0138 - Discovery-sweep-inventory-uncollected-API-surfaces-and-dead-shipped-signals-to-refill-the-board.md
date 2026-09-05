---
id: TSO-0138
title: >-
  Discovery sweep: inventory uncollected API surfaces and dead shipped signals
  to refill the board
status: Done
assignee:
  - '@codex'
created_date: '2026-09-05 17:16'
updated_date: '2026-09-05 18:41'
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
- [x] #1 A ledger with a fixed schema (source, surface, consumed-by or none, evidence, proposed disposition) covers every GET operation in the vendored spec and every Border0 endpoint in the PAM reference doc section 2, with no row left blank
- [x] #2 Every shipped metric and log family is checked for samples on the stack over the trailing 30 days through read-only queries, and each zero-sample family is listed with a proposed disposition: dead signal, lab-shape gap, or expected-quiet
- [x] #3 Each candidate the root adopts is filed as a To Do task labelled needs-triage with a description stating the need and testable acceptance criteria; each rejected candidate is recorded on this task with its reason
- [x] #4 No candidate re-proposes a surface that already carries a rejected or adopted verdict in spec/changelog-reviewed.json, or a Done task, without new evidence stated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 13 front-loaded Codex campaign: root owns tracker, config seams, integration, gates and explicit-path commits. Four flat lanes use the frozen goal sections 0, 4, 5 and 6; A/C/D overlap phase 0, B waits for its local commit and C acceptance before option wiring. Verify focused contracts, generated stability, full gate, CodeRabbit, exact-head CI/auto-rc and read-only stdout live runs before reconciliation and terminal file report.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovery adjudication: adopted log-stream destination configuration as TSO-0139 (highest priority); new evidence is the unused vendored GET and its bounded destinationType/configuration fields, distinct from existing status counters. Rejected OAuth-app count: already emitted by internal/collector/oauthapps/oauthapps.go:135 and wired in internal/app/collectors.go:284. Rejected organization-tailnet count: already emitted in internal/app/organization.go:20-25 (TSO-0034 Done). Rejected Border0 serverinfo delay gauge: rx_after_tx_delay_ms is a configured read-after-write consistency delay, not measured observation lag, and this collector never writes. Rejected service-account token accumulation/age signal for this wave: no new evidence of operational harm or stable expiry semantics beyond the already-adopted PAM inventory work; do not re-propose broad adopted PAM surface from an unused client method alone. Five seeded-random collected-row checks covered service hosts, connectors, POSTURE_INTEGRATION, NODE and users; source confirms their consumers, with NODE evidence corrected to ChangeCategory rather than the unrelated PAM map.

Read-only 30-day stack sweep completed: 331 catalog metric families and 30 catalog log families checked; 245 present, 116 absent. Metric names came from time-bounded label-values scoped to the exporter service; log records from count_over_time grouped by event_name. The initial broad series query exceeded the 50 MB client response cap and was replaced with label-values (new evidence, no identical retry). Every absent family is listed in the ignored ledger with expected-quiet or lab-shape-gap proposed disposition; absence does not establish dead code or a deployment fault. No Grafana write performed.

Source-only discovery ledger reconciles 115 rows: 35 vendored GETs, 18 Border0 endpoints, 4 open questions, 45 audit target.property values, 12 target.type values and 1 parked changelog verdict. The stack extension adds 331 metric-family and 30 log-family rows. Final integrated local gate passed; generated bytes were stable. Candidate TSO-0139 remains To Do with needs-triage; all four rejected candidates and the five seeded consumer checks are recorded here.

Verified integration base SHA c94ba92dbfa47f8f99c527cb8fc7ea9a032a8f4f; CI 33984156766 succeeded on attempt 1. All source and stack rows reconciled, and TSO-0139 was read back as To Do with needs-triage and four testable criteria. This task and all four closeouts form the following tracker-only commit, whose SHA is recorded in the terminal report. CodeRabbit and new tests are skipped for that documentation-only closeout.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Completed a 115-row source inventory and 361-family 30-day stack sweep. Adopted safe log-stream destination configuration as TSO-0139; rejected four candidates with source evidence or missing-operational-evidence rationale. Recorded all 116 sample absences as proposed quiet/lab-shape gaps rather than proven dead signals.
<!-- SECTION:FINAL_SUMMARY:END -->
