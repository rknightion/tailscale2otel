---
id: TSO-0012
title: Revalidate and align the Alloy gateway recipe
status: To Do
assignee: []
created_date: '2026-08-26 11:02'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - deploy/alloy/README.md
  - deploy/alloy/docker-compose.yaml
  - deploy/alloy/config.alloy
priority: medium
type: bug
ordinal: 16000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The runnable Compose recipe pins Grafana Alloy v1.19.1 while its README, config comments, and validation commands claim v1.18.0. Validate the deployed version under the documented smoke and outage conditions, then make every claim and command describe that evidence.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Compose, config comments, README claims, and validation commands name one tested Alloy version
- [ ] #2 The config is syntax-validated by that exact container image
- [ ] #3 The documented readiness, retry, persistent-queue, and outage-drill checks are rerun against the aligned recipe
- [ ] #4 Every follow-up Compose command works with the documented env-file location
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
