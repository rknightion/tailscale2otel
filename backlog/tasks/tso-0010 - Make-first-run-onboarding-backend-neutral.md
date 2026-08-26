---
id: TSO-0010
title: Make first-run onboarding backend-neutral
status: To Do
assignee: []
created_date: '2026-08-26 11:01'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies:
  - TSO-0007
  - TSO-0008
  - TSO-0009
references:
  - README.md
  - docs/getting-started.md
  - docs/installation.md
priority: high
type: docs
ordinal: 14000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
README, the docs landing page, Getting Started, and Installation currently lead with Grafana Cloud credentials even though OTLP, Prometheus pull, and stdout are all supported. Rebuild the first-ten-minutes journey around a destination choice, define the minimum OpenTelemetry vocabulary, and remove duplicate executable quickstarts that have already drifted.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Before destination-specific credentials, readers can distinguish OpenTelemetry, OTLP, a Collector, Grafana Cloud, Prometheus pull, and stdout in plain language
- [ ] #2 Grafana OTLP, Prometheus pull, and stdout each reach a first observable signal with a runnable configuration and expected result
- [ ] #3 Prometheus guidance covers listener authentication, /metrics verification, scraper verification, and avoidance of duplicate OTLP plus pull ingestion into the same backend
- [ ] #4 Each deployment and destination combination has one canonical executable snippet; README and the docs index link to it rather than copying it
- [ ] #5 README, docs index, Getting Started, Installation, Comparison, FAQ, and Troubleshooting form one linked route and documented commands pass the repository checker
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
