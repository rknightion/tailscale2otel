---
id: TSO-0141
title: >-
  Bring the lab configuration up to the shipped feature set and adjudicate the
  zero-sample signal families
status: To Do
assignee: []
created_date: '2026-09-05 20:12'
updated_date: '2026-09-05 20:13'
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
- [ ] #1 Each of the 116 absent families carries one recorded verdict: now-present, expected-quiet with the condition that would make it fire, unexercisable-on-lab with the reason, or dead (which becomes its own task); no family is left with the Wave 13 boilerplate note
- [ ] #2 The lab values enable PAM, the snapshot logs, the device change-log, the OAuth-app inventory and the Kubernetes audit collector where the lab can support them, landed in the infra repo and cited by commit, with the PAM token present in the lab secret path
- [ ] #3 A re-run of the 30-day presence check after the pod has restarted on a current image shows the newly enabled families present, with the before and after counts recorded here
- [ ] #4 Any family found dead is filed as a task with the evidence, and the coverage manifest is not edited to hide it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
