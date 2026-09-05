---
id: TSO-0141
title: >-
  Bring the lab configuration up to the shipped feature set and adjudicate the
  zero-sample signal families
status: Done
assignee: []
created_date: '2026-09-05 20:12'
updated_date: '2026-09-05 22:06'
labels: []
dependencies:
  - TSO-0140
references:
  - codex/ledger-2026-09-05-wave13.md
priority: medium
type: chore
ordinal: 142000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 13's read-only 30-day stack sweep found 95 metric and 21 log families shipped by this repository with no samples on the stack. The lab values in the private infrastructure GitOps repository enable none of: the PAM collector (no pam block, no PAM token in the lab secret), the objectstore ingestion paths, any snapshot_enabled log, the device change_log, the Kubernetes audit collector, or the OAuth-app inventory. Absent families are recorded in the ignored Wave 13 ledger with boilerplate dispositions that were not diagnosed. Only a lab that runs the shipped features can tell a dead signal from an unexercised one. Enable each feature the lab can support (the PAM read-only service-account token must be placed in the lab secret path by the owner or with explicit authority), leave a written verdict for each family that cannot be exercised there, and re-run the presence check. Live work on the lab stays with the root agent. No lab identifiers in this task or the tracker.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Each of the 116 absent families carries one recorded verdict: now-present, expected-quiet with the condition that would make it fire, unexercisable-on-lab with the reason, or dead (which becomes its own task); no family is left with the Wave 13 boilerplate note
- [x] #2 The lab values enable PAM, the snapshot logs, the device change-log, the OAuth-app inventory and the Kubernetes audit collector where the lab can support them, landed in the infra repo and cited by commit, with the PAM token present in the lab secret path
- [x] #3 A re-run of the 30-day presence check after the pod has restarted on a current image shows the newly enabled families present, with the before and after counts recorded here
- [x] #4 Any family found dead is filed as a task with the evidence, and the coverage manifest is not edited to hide it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Enable every supported lab surface through the private infra values and existing secret mapping, roll the current release through Argo, wait for every enabled collector to succeed, repeat the service-scoped 30-day presence method, adjudicate all prior absences, and persist the ledger as a tracker document.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Infra commits 2aa339c9d70a9eb9190b32a86943fe2e3aa6cd81 and 992f0c15a31818dafb2505d57294efd249aa1be3 enabled the supported shipped surfaces, added only the PAM token mapping, and pinned the first RC; c211ce7c00bc7f614f1d12aacff11bc97029bbc1 rolled RC.27. AWS secret properties increased 13 to 14 and the Kubernetes Secret mapped keys increased 12 to 13; all 16 collectors ran successfully. The repeated 2026-08-06T21:56:10Z to 2026-09-05T21:56:10Z query found 263/332 metrics and 16/30 logs present, versus 236/331 and 9/30 in Wave 13. Of the original 116 absences, doc-0005 records 33 now-present, 39 expected-quiet, 43 unexercisable-on-lab and one dead. The dead scope-preflight family is filed as TSO-0142; the coverage manifest was not changed to hide it.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Made the lab current and enabled every supported shipped surface, including PAM and snapshot/change-log paths. All collectors succeeded on RC.27; the repeated 30-day audit gained 33 formerly absent families and gives every original absence a concrete verdict in doc-0005, with the one dead runtime signal filed as TSO-0142.
<!-- SECTION:FINAL_SUMMARY:END -->
