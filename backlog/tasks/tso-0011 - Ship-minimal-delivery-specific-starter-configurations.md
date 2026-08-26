---
id: TSO-0011
title: Ship minimal delivery-specific starter configurations
status: To Do
assignee: []
created_date: '2026-08-26 11:01'
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
- [ ] #1 Small starters exist for Grafana Cloud OTLP, Prometheus-only, and stdout smoke testing
- [ ] #2 The Prometheus starter needs only Tailscale authentication and an explicit scrape-exposure choice
- [ ] #3 Each starter states which credentials are values versus *_file mounts and points to -preflight for authenticated proof
- [ ] #4 Tests load and validate every starter with placeholder secrets, and generated/reference ownership is documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
