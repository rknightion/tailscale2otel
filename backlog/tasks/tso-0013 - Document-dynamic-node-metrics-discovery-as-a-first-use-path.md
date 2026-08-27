---
id: TSO-0013
title: Document dynamic node-metrics discovery as a first-use path
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-26 11:02'
updated_date: '2026-08-27 06:48'
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
- [x] #1 The guide includes valid discovery-only YAML with enabled plus discovery settings
- [x] #2 It explains the online, non-external, tag, address-family, port, and maximum-target defaults
- [x] #3 It states the client-metrics endpoint and ACL prerequisites and how discovered and static targets combine
- [x] #4 A first-success query confirms discovered targets and forwarded metrics
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add a discovery-first node-metrics path from live config defaults, preserving static targets and documenting a first-success query.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovery-first configuration, defaults, prerequisites, and first-success query are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522. Resume with exact-head CI and live documentation verification.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Dynamic node-metrics discovery guidance is committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending CI and live verification.
<!-- SECTION:FINAL_SUMMARY:END -->
