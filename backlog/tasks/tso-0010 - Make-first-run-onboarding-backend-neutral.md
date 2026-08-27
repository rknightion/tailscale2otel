---
id: TSO-0010
title: Make first-run onboarding backend-neutral
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-26 11:01'
updated_date: '2026-08-27 06:48'
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
- [x] #1 Before destination-specific credentials, readers can distinguish OpenTelemetry, OTLP, a Collector, Grafana Cloud, Prometheus pull, and stdout in plain language
- [x] #2 Grafana OTLP, Prometheus pull, and stdout each reach a first observable signal with a runnable configuration and expected result
- [x] #3 Prometheus guidance covers listener authentication, /metrics verification, scraper verification, and avoidance of duplicate OTLP plus pull ingestion into the same backend
- [x] #4 Each deployment and destination combination has one canonical executable snippet; README and the docs index link to it rather than copying it
- [x] #5 README, docs index, Getting Started, Installation, Comparison, FAQ, and Troubleshooting form one linked route and documented commands pass the repository checker
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Rebuild the first-ten-minutes route around the frozen destination chooser and starter/check seams; make one canonical runnable snippet per deployment/destination and link README/index/comparison/FAQ/troubleshooting/configuration.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Backend-neutral onboarding and canonical deployment snippets are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; the documentation command checker passed. Resume with exact-head CI and live first-signal verification for each destination.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Backend-neutral first-run documentation is committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked at the live and exact-head verification boundary.
<!-- SECTION:FINAL_SUMMARY:END -->
