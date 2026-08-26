---
id: TSO-0009
title: Add a bounded Prometheus first-scrape check
status: To Do
assignee: []
created_date: '2026-08-26 11:01'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - cmd/tailscale2otel/preflight.go
  - internal/app/metrics.go
priority: medium
type: feature
ordinal: 13000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The CLI can validate configuration and run an authenticated collection preflight, but it cannot prove that a Prometheus operator will get a usable scrape without starting the long-running service.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bounded CLI check runs one collection cycle and verifies that the Prometheus registry contains a documented sentinel and valid exposition
- [ ] #2 Human and JSON output distinguish configuration, authentication, collection, gather, and access-posture failures
- [ ] #3 The check does not start a long-lived listener, persist checkpoints, mutate the control plane, or deliver OTLP unless explicitly requested
- [ ] #4 Command tests cover successful and failing results
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
