---
id: TSO-0174
title: >-
  Move to the tailscale.com/client/tailscale/v2 Go client and repoint the
  client-lib lane
status: In Progress
assignee:
  - loop27
created_date: '2026-10-11 10:40'
updated_date: '2026-10-11 15:35'
labels: []
dependencies:
  - TSO-0173
references:
  - 'https://github.com/tailscale/tailscale-client-go-v2'
priority: high
type: chore
ordinal: 174000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
go.mod pins github.com/tailscale/tailscale-client-go/v2 at a 2025-01-29 pseudo-version, and that module path is frozen: @main and @latest both resolve to the same commit. Upstream publishes tagged releases under tailscale.com/client/tailscale/v2 (v2.12.0, 2026-10-09), which types newer settings such as RouteSelection. The weekly client-lib CI lane watches the dead path, which is the likely reason it has reported inconclusive on every recent scheduled run. A note on TSO-0116 claims the client has no tagged releases; that is no longer true.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 go.mod requires tailscale.com/client/tailscale/v2 at a tagged release and no Go file, workflow or Renovate rule references tailscale-client-go
- [x] #2 just check passes across all five modules
- [ ] #3 The client-lib lane resolves the new module and a manual dispatch returns a conclusive verdict on the unchanged pin
- [x] #4 TSO-0116 carries an appended note correcting the no-tagged-releases claim
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Move all root/tool-module/import/workflow references to the current tagged Tailscale client in one candidate; confirm symbol shapes with go doc and tidy each of five modules without changing the OTEL lockstep set. Return staged candidate for guarded review, CodeRabbit and serialized exact full gate. Root corrects the historical task note, lands, runs fresh composed gate and exact-SHA CI, then performs one manual client-lib dispatch and records its conclusive verdict.

loop27: one fresh root workflow dispatch; mapper retains both verdicts and exact run identity.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
AC #4 root prerequisite complete: appended correction to TSO-0116 (adjudicate the client-lib drift alert despite passing matrix legs), preserving its history and explaining tagged tailscale.com/client/tailscale/v2 releases. Implementation attempt 1 dispatched in isolated retained client worktree on 68f43d77e04c980c109c92e741124199eb3a623d; no workflow dispatch yet.

loop26 post-land proof: the one authorized manual dispatch 38141530513 at 3546e554534cba20d0dc4412e49ade0e4097d0c9 failed report with two actual verdict=inconclusive artifacts. Both latest and main resolved baseline/candidate v2.12.0, but TestToolPinAssertions and otlpackprobe TestRecipeSecurityRegression failed because just was absent on the build runners. This is setup failure, not upstream drift; required ci-success run 38140703181 and helm-success run 38140703568 remain green. Root is repairing the authorized prerequisite by wiring ci.yml existing unchanged setup-just installer/version before the client-lib tests. No existing tool pin, gate or assertion is changed. Fresh manual proof will require authority for another dispatch; rerunning the immutable old run cannot test the new bootstrap. AC #3 remains unchecked.

loop26: implementation attempts 2 (migration plus small bootstrap prerequisite); review-repair rounds 1. Module migration 3546e554534cba20d0dc4412e49ade0e4097d0c9 and missing-just prerequisite 9bb62a0d1cd66996e30c62b5555d25b2a12e5c08 landed after exact gates/guarded review; original 38-path CodeRabbit completed clean, four-line pure wiring delta skipped. Existing installer/action/version reused unchanged, no protected pin/assertion altered. Bot dependency merge 627a53798d400b13cbe9a1e09bb0d9718ed78155 preserved and verified with fresh composed G exit 0 in 571.156s, ci-success 38142864153 and helm-success 38142865358 green. Cancelled bootstrap CI 38142799445 and skipped RC 38142893241 are not passes. Single authorized manual run 38141530513 returned actual latest/main verdict=inconclusive on unchanged v2.12.0 because just was missing; both successful job statuses were NOT successful tests/verdicts. Bootstrap is now wired before those tests. Park needs=authority: one-operation grant consumed, so AC #3 stays unchecked. Resume with explicit authority for one fresh clientlib-main.yml workflow_dispatch on repaired current main; inspect both real verdict artifacts for conclusive unchanged-pin outcomes, then finalize. Historical TSO-0116 (adjudicate client-lib drift despite passing matrix legs) correction is appended and preserved.

loop27 fresh live client-lib proof accepted: workflow dispatch run 38151283023 at d57fdc9405c4ee2dcf7ebcebb998b234a3baca3d completed success. latest and main verdicts both pass; each resolved unchanged baseline/candidate v2.12.0 with go get and test exit 0. Root read artifacts and exact run metadata. No new source change or implementation attempt; finalization follows composed source gate. CodeRabbit not applicable to artifact retrieval.
<!-- SECTION:NOTES:END -->
