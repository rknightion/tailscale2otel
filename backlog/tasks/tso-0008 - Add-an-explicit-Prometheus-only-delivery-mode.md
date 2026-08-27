---
id: TSO-0008
title: Add an explicit Prometheus-only delivery mode
status: Done
assignee:
  - '@codex'
created_date: '2026-08-26 11:01'
updated_date: '2026-08-27 07:34'
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
- [x] #1 One configuration choice enables Prometheus metrics without requiring separate OTLP metrics, logs, and traces disable switches
- [x] #2 Prometheus collection and self-observability remain available on /metrics while no OTLP export attempt is made
- [x] #3 Logs and traces have an explicit documented disposition rather than silently targeting the default OTLP endpoint
- [x] #4 Tests cover Prometheus-only and existing dual-delivery operation
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement the frozen delivery.mode contract across config, telemetry, schema and deployment surfaces; prove Prometheus-only suppresses inherited OTLP while dual mode remains compatible.

Post-delivery cleanup requested by the human: restore the live deployment's captured pre-run OTLP posture, retain its pre-existing authenticated local-validation listener configuration, delete the campaign-created Fleet scrape pipeline, and independently read back both states.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented and locally validated in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522. Emergency stop occurred before final security, exact-head CI, and live Prometheus-only proof. Resume from that commit without redesigning the frozen delivery.mode seam.

Final evidence: integrated GATE and exact-head CI run 33047209645 succeeded. The live deployment used delivery.mode=prometheus; post-restart logs showed no OTLP export attempts, while the loopback Prometheus sentinel was scraped through Fleet and queried from the metrics backend.

Post-delivery cleanup requested by the human is complete. The live deployment was restored to its captured pre-run OTLP posture while retaining the pre-existing authenticated local-validation listener. The campaign-created Fleet scrape pipeline was deleted. Independent read-back showed a running deployment, authenticated metrics HTTP 200, no delivery override, zero recent export errors, and zero matching Fleet pipelines.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Prometheus-only and dual delivery dispositions are committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending final security, CI, and live proof.

Completion: verified by tests, exact-head CI, and live Prometheus-only delivery.
<!-- SECTION:FINAL_SUMMARY:END -->
