---
id: TSO-0015
title: Add an operator-first alert profile chooser
status: To Do
assignee: []
created_date: '2026-08-26 11:02'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - docs/alerts.md
  - docs/alert-profiles.md
priority: medium
type: docs
ordinal: 19000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Alert Profiles defines baseline, recommended, and strict, but the main Alerts guide leads directly into deployment and manifest internals without helping an operator choose a starting set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Alerts links to Alert Profiles before deployment internals and identifies the committed set as recommended
- [ ] #2 Alert rule, recording rule, paused, and profile are defined in operational terms before schema details
- [ ] #3 The profile page remains generated and its drift test stays green
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
