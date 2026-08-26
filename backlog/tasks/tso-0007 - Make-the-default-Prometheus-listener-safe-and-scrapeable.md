---
id: TSO-0007
title: Make the default Prometheus listener safe and scrapeable
status: To Do
assignee: []
created_date: '2026-08-26 11:01'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - internal/config/defaults.go
  - internal/app/metrics.go
  - config.example.yaml
priority: high
type: bug
ordinal: 11000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Enabling Prometheus currently retains the wildcard :2112 listener. Tokenless requests then validate with a warning but return HTTP 403, so the smallest apparent opt-in starts successfully and cannot be scraped. Make the minimal local opt-in safe and useful without weakening network-reachable authentication.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 With only Prometheus enabled and no token, the default listener is loopback-only and GET /metrics from the same host returns 200
- [ ] #2 A network-reachable bind still requires a token or the explicit allow_unauthenticated acknowledgement
- [ ] #3 A configured token is enforced on loopback and network-reachable binds
- [ ] #4 Defaults, example config, generated env reference, validation warnings, and focused auth tests agree
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
