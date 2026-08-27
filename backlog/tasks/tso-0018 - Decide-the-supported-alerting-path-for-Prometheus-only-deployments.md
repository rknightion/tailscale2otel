---
id: TSO-0018
title: Decide the supported alerting path for Prometheus-only deployments
status: Done
assignee:
  - '@codex'
created_date: '2026-08-26 11:02'
updated_date: '2026-08-27 07:01'
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
- [x] #1 The task records whether Prometheus-compatible rule deployment is supported, deliberately unsupported, or deferred with named blockers
- [x] #2 The decision covers normalized metric-name portability, recording rules, runbook links, and how generated artifacts avoid drift
- [x] #3 User-facing alert and Prometheus documentation states the chosen support boundary without presenting test fixtures as deployable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Promote the Prometheus rule rendering to the frozen committed artifact, preserve normalized names/recording rules/runbooks, and execute it with promtool fixtures.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Supported Prometheus rules, drift generation, normalized names, recording rules, runbooks, and semantic fixtures are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522. Promtool execution passed with the pinned Prometheus container. Resume with exact-head CI and live rule deployment verification.

Final evidence: pinned Prometheus v3.7.3 executed all five rule fixtures successfully; promqlcheck passed; exact-head CI run 33047209645 succeeded. All 124 Grafana-managed resources pushed successfully; scoped post-push verification reported 123 shipped and deployed rules with zero missing, orphaned, or drifted.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Supported Prometheus alert rules are committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending CI and live deployment proof.

Completion: verified by executable fixtures, exact-head CI, and live alert deployment.
<!-- SECTION:FINAL_SUMMARY:END -->
