---
id: TSO-0009
title: Add a bounded Prometheus first-scrape check
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
  - cmd/tailscale2otel/preflight.go
  - internal/app/metrics.go
priority: medium
type: feature
ordinal: 13000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The CLI can validate configuration and run an authenticated collection preflight, but it cannot prove that a Prometheus operator will get a usable scrape without starting the long-running service.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A bounded CLI check runs one collection cycle and verifies that the Prometheus registry contains a documented sentinel and valid exposition
- [x] #2 Human and JSON output distinguish configuration, authentication, collection, gather, and access-posture failures
- [x] #3 The check does not start a long-lived listener, persist checkpoints, mutate the control plane, or deliver OTLP unless explicitly requested
- [x] #4 Command tests cover successful and failing results
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add -prometheus-check on the frozen RunPrometheusOnce seam; classify configuration, authentication, collection, gather and access-posture failures in human and JSON output; prove bounded side-effect-free behavior.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented and locally validated in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522, including zero-annotation and zero-OTLP side-effect tests. Resume with final security review, exact-head CI, and a live bounded check.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
The bounded classified Prometheus first-scrape check is committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending final security, CI, and live proof.
<!-- SECTION:FINAL_SUMMARY:END -->
