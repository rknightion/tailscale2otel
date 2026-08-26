---
id: TSO-0012
title: Revalidate and align the Alloy gateway recipe
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-26 11:02'
updated_date: '2026-08-26 16:58'
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
- [x] #1 Compose, config comments, README claims, and validation commands name one tested Alloy version
- [x] #2 The config is syntax-validated by that exact container image
- [ ] #3 The documented readiness, retry, persistent-queue, and outage-drill checks are rerun against the aligned recipe
- [x] #4 Every follow-up Compose command works with the documented env-file location
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Align the Alloy recipe on one validated image version and syntax-check that exact image; root will perform the live outage drill.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Alloy v1.19.1 alignment and exact-image syntax validation are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522. Acceptance criterion 3 remains unchecked: the readiness, retry, persistent-queue, and outage drill was not run because the campaign was stopped. Resume by resolving the lab credential input and executing the documented drill.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Alloy version and commands are aligned in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked with the live outage drill still required.
<!-- SECTION:FINAL_SUMMARY:END -->
