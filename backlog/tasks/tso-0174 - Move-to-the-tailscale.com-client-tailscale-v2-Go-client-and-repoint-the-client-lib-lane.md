---
id: TSO-0174
title: >-
  Move to the tailscale.com/client/tailscale/v2 Go client and repoint the
  client-lib lane
status: In Progress
assignee:
  - loop26
created_date: '2026-10-11 10:40'
updated_date: '2026-10-11 13:16'
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
- [ ] #1 go.mod requires tailscale.com/client/tailscale/v2 at a tagged release and no Go file, workflow or Renovate rule references tailscale-client-go
- [ ] #2 just check passes across all five modules
- [ ] #3 The client-lib lane resolves the new module and a manual dispatch returns a conclusive verdict on the unchanged pin
- [ ] #4 TSO-0116 carries an appended note correcting the no-tagged-releases claim
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
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
AC #4 root prerequisite complete: appended correction to TSO-0116 (adjudicate the client-lib drift alert despite passing matrix legs), preserving its history and explaining tagged tailscale.com/client/tailscale/v2 releases. Implementation attempt 1 dispatched in isolated retained client worktree on 68f43d77e04c980c109c92e741124199eb3a623d; no workflow dispatch yet.

loop26 post-land proof: the one authorized manual dispatch 38141530513 at 3546e554534cba20d0dc4412e49ade0e4097d0c9 failed report with two actual verdict=inconclusive artifacts. Both latest and main resolved baseline/candidate v2.12.0, but TestToolPinAssertions and otlpackprobe TestRecipeSecurityRegression failed because just was absent on the build runners. This is setup failure, not upstream drift; required ci-success run 38140703181 and helm-success run 38140703568 remain green. Root is repairing the authorized prerequisite by wiring ci.yml existing unchanged setup-just installer/version before the client-lib tests. No existing tool pin, gate or assertion is changed. Fresh manual proof will require authority for another dispatch; rerunning the immutable old run cannot test the new bootstrap. AC #3 remains unchecked.
<!-- SECTION:NOTES:END -->
