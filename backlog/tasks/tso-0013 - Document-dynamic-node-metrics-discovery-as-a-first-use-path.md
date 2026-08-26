---
id: TSO-0013
title: Document dynamic node-metrics discovery as a first-use path
status: To Do
assignee: []
created_date: '2026-08-26 11:02'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - docs/node-metrics.md
  - internal/collector/nodemetrics
priority: medium
type: docs
ordinal: 17000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The product advertises automatic target discovery, but the node-metrics guide teaches only static targets. Add a complete discovery-first path without displacing the static configuration.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The guide includes valid discovery-only YAML with enabled plus discovery settings
- [ ] #2 It explains the online, non-external, tag, address-family, port, and maximum-target defaults
- [ ] #3 It states the client-metrics endpoint and ACL prerequisites and how discovered and static targets combine
- [ ] #4 A first-success query confirms discovered targets and forwarded metrics
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
