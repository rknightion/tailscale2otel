---
id: TSO-0008
title: Add an explicit Prometheus-only delivery mode
status: To Do
assignee: []
created_date: '2026-08-26 11:01'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - internal/telemetry/provider.go
  - internal/app/options.go
  - docs/configuration.md
priority: high
type: feature
ordinal: 12000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Prometheus pull is currently an additional metric reader while the default OTLP metrics and logs exporters remain active. A Prometheus-only operator must discover and disable three per-signal OTLP paths. Add one explicit configuration choice that has a documented disposition for metrics, logs, and traces.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 One configuration choice enables Prometheus metrics without requiring separate OTLP metrics, logs, and traces disable switches
- [ ] #2 Prometheus collection and self-observability remain available on /metrics while no OTLP export attempt is made
- [ ] #3 Logs and traces have an explicit documented disposition rather than silently targeting the default OTLP endpoint
- [ ] #4 Tests cover Prometheus-only and existing dual-delivery operation
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
