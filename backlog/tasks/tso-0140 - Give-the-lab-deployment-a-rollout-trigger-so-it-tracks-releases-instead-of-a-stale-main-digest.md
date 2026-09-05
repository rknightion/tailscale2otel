---
id: TSO-0140
title: >-
  Give the lab deployment a rollout trigger so it tracks releases instead of a
  stale main digest
status: Done
assignee:
  - '@codex'
created_date: '2026-09-05 20:12'
updated_date: '2026-09-05 22:46'
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
- [x] #1 The owner-chosen mechanism is recorded here with its reason, and the infra repo change that implements it is landed and cited by commit
- [x] #2 After the change the live lab pod runs the image digest that the mechanism selects, verified by reading the pod spec and comparing against the registry, and the next RC (or a forced trigger) rolls it again without a manual kubectl step
- [x] #3 The Wave operating model doc states how the lab picks up a new image so a wave can plan live verification against it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Pin the lab exporter to the current RC, add a Renovate regex manager and automerge rule, validate the rendered lab configuration and infra gate, then prove the selected digest rolled through Argo without a manual Deployment edit.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner decision 2026-09-05: Renovate-pinned rc tag (option 1). The infra repo tracks the same work as EKS-0071, whose description previously said the tag must stay on main; that paragraph is superseded by this decision and the note was appended there.

Owner-selected Renovate-pinned RC tags landed in infra commits 2aa339c9d70a9eb9190b32a86943fe2e3aa6cd81 and 992f0c15a31818dafb2505d57294efd249aa1be3; c211ce7c00bc7f614f1d12aacff11bc97029bbc1 advanced the lab to RC.27. The manager detected ghcr.io/rknightion/tailscale2otel using semver-coerced ordering with prereleases enabled. Infra `just check` passed. Notify Argo runs 33991646520, 33991697382 and 33994252971 succeeded. The live pod rolled without a manual Deployment edit, became ready with zero restarts, and its image ID matched RC.27 registry digest sha256:ba02dd35e6f67009f7c6ee2c266b96bb8d70dc5f475909862548360af6e33b8b. The Wave operating model is updated in doc-0002.

Owner decision 2026-09-05, answering the Wave 14 report: the lab keeps taking RC tags after 5.0.0; the Renovate rule (ignoreUnstable false, semver-coerced) stays as landed. First observed Renovate RC bump PR on the infra repo is still the outstanding evidence for AC 2.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Replaced the stale mutable main image with an auditable Renovate-managed RC tag. Two successive RC selections rolled automatically through Argo, and the running RC.27 image matched the registry digest; doc-0002 now records the mechanism and proof contract.
<!-- SECTION:FINAL_SUMMARY:END -->
