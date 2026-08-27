---
id: TSO-0011
title: Ship minimal delivery-specific starter configurations
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
  - TSO-0008
references:
  - config.example.yaml
  - docs/configuration.md
priority: high
type: enhancement
ordinal: 15000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The 700-plus-line config.example.yaml is a useful exhaustive reference but is too large to be the first configuration a new operator edits. Add small, independently valid starters for the supported first-run destinations while keeping the exhaustive example authoritative.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Small starters exist for Grafana Cloud OTLP, Prometheus-only, and stdout smoke testing
- [x] #2 The Prometheus starter needs only Tailscale authentication and an explicit scrape-exposure choice
- [x] #3 Each starter states which credentials are values versus *_file mounts and points to -preflight for authenticated proof
- [x] #4 Tests load and validate every starter with placeholder secrets, and generated/reference ownership is documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add the three frozen examples/config starters and a real config.Load validation test with placeholder secrets; document value versus *_file credentials and point each starter to -preflight.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
All three starters load and validate through config.Load and are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522. Resume with exact-head CI and live first-signal proof.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Grafana OTLP, Prometheus-only, and stdout starters are committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending CI and live proof.
<!-- SECTION:FINAL_SUMMARY:END -->
