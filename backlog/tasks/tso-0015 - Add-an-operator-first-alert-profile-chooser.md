---
id: TSO-0015
title: Add an operator-first alert profile chooser
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
- [x] #1 Alerts links to Alert Profiles before deployment internals and identifies the committed set as recommended
- [x] #2 Alert rule, recording rule, paused, and profile are defined in operational terms before schema details
- [x] #3 The profile page remains generated and its drift test stays green
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add the operator-first alert profile chooser and definitions while preserving the generated profile page and its drift check.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The operator-first chooser and definitions are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; generated alert-profile checks passed. Resume with exact-head CI.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
The alert-profile chooser is committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending exact-head CI.
<!-- SECTION:FINAL_SUMMARY:END -->
