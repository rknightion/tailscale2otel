---
id: TSO-0018
title: Decide the supported alerting path for Prometheus-only deployments
status: To Do
assignee: []
created_date: '2026-08-26 11:02'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - deploy/alerts/README.md
  - docs/alerts.md
priority: medium
type: spike
ordinal: 22000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The project exports Prometheus metrics but ships only Grafana-managed alert rules. The Prometheus rendering is currently test-only, leaving Prometheus-only operators without a declared alerting route.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The task records whether Prometheus-compatible rule deployment is supported, deliberately unsupported, or deferred with named blockers
- [ ] #2 The decision covers normalized metric-name portability, recording rules, runbook links, and how generated artifacts avoid drift
- [ ] #3 User-facing alert and Prometheus documentation states the chosen support boundary without presenting test fixtures as deployable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
