---
id: TSO-0174
title: >-
  Move to the tailscale.com/client/tailscale/v2 Go client and repoint the
  client-lib lane
status: To Do
assignee: []
created_date: '2026-10-11 10:40'
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
