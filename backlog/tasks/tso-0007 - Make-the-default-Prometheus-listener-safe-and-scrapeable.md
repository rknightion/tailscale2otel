---
id: TSO-0007
title: Make the default Prometheus listener safe and scrapeable
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-26 11:01'
updated_date: '2026-08-26 16:58'
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
- [x] #1 With only Prometheus enabled and no token, the default listener is loopback-only and GET /metrics from the same host returns 200
- [x] #2 A network-reachable bind still requires a token or the explicit allow_unauthenticated acknowledgement
- [x] #3 A configured token is enforced on loopback and network-reachable binds
- [x] #4 Defaults, example config, generated env reference, validation warnings, and focused auth tests agree
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement the frozen loopback listener default and auth matrix test-first; regenerate config and Helm references; verify focused and repository Go gates.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented and locally validated in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522. Emergency stop occurred before the final clean SECURITY verdict, exact-head CI, and live scrape proof. Resume by rerunning the read-only security review on this exact tree, then exact-head CI and live verification.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Safe loopback defaults, fail-closed remote exposure, effective-token Helm validation, and auth tests are committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending final security, CI, and live proof.
<!-- SECTION:FINAL_SUMMARY:END -->
