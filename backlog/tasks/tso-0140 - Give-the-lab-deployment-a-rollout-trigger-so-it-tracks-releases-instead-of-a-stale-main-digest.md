---
id: TSO-0140
title: >-
  Give the lab deployment a rollout trigger so it tracks releases instead of a
  stale main digest
status: To Do
assignee: []
created_date: '2026-09-05 20:12'
updated_date: '2026-09-05 20:34'
labels: []
dependencies: []
references:
  - codex/ledger-2026-09-05-wave13.md
priority: medium
type: chore
ordinal: 141000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The lab is a Helm release managed by an Argo application in the private infrastructure GitOps repository, and that application pins the image to the mutable main tag with pullPolicy Always. A pod only pulls on restart and nothing restarts it when main moves, so on 2026-09-05 the running digest was seven days old and predated the PAM collector, the pin assertions and every Wave 12-13 change. Wave 13's 30-day stack sweep therefore found 116 shipped signal families with no samples, most of which cannot be judged until the lab actually runs current code. The infra repo already runs Renovate with custom managers for other helm values, so pinning the tag to the latest v5.0.0-rc.N (and later the stable tag) with a Renovate regex manager would roll the lab on every RC; the alternative is a scheduled or CI-triggered rollout restart. The owner picks the mechanism. Writes land in the infra repo, not here, and the Argo app has prune and selfHeal enabled so a manual kubectl edit is undone.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The owner-chosen mechanism is recorded here with its reason, and the infra repo change that implements it is landed and cited by commit
- [ ] #2 After the change the live lab pod runs the image digest that the mechanism selects, verified by reading the pod spec and comparing against the registry, and the next RC (or a forced trigger) rolls it again without a manual kubectl step
- [ ] #3 The Wave operating model doc states how the lab picks up a new image so a wave can plan live verification against it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner decision 2026-09-05: Renovate-pinned rc tag (option 1). The infra repo tracks the same work as EKS-0071, whose description previously said the tag must stay on main; that paragraph is superseded by this decision and the note was appended there.
<!-- SECTION:NOTES:END -->
