---
id: TSO-0020
title: Rotate credentials exposed in an agent transcript
status: Done
assignee: []
created_date: '2026-08-27 07:08'
updated_date: '2026-08-27 07:47'
labels:
  - needs-triage
  - security
dependencies: []
priority: high
type: task
ordinal: 24000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live control-plane and observability credential values were exposed in an agent transcript during an authorized delivery run. A human must rotate the affected credentials and verify that the replaced values can no longer authenticate. No credential values or environment-specific identifiers belong in this task.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The human identifies and rotates every credential value exposed in the transcript
- [x] #2 The replaced credential values are independently confirmed unable to authenticate
- [x] #3 All authorized live consumers are updated to use replacement credentials without committing secret material
- [x] #4 The rotation and verification outcome is recorded here without real identifiers or credential values
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
