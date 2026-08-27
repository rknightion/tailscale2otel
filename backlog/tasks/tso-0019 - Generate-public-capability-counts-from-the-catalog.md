---
id: TSO-0019
title: Generate public capability counts from the catalog
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
  - internal/catalog
  - README.md
  - docs/index.md
priority: low
type: enhancement
ordinal: 23000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
README and entry pages repeatedly hard-code metric, log-event, collector, dashboard, and rule counts. These numbers had diverged across the public documentation. Derive or check the summary values from the same sources that generate the detailed artifacts.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A deterministic source provides the public metric, log-event, collector, dashboard, alert-rule, and recording-rule counts
- [x] #2 README, docs index, Comparison, FAQ, and navigation summaries cannot drift silently from those sources
- [x] #3 The check fails with an actionable regeneration or edit instruction
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Derive six public capability counts from catalog and shipped artifacts, add the frozen generated source/checker, then reconcile every owned numeric summary.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Generated capability counts and public-summary drift checks are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522. Current derived values are 293 metrics, 17 log events, 16 collectors, 2 dashboards, 100 alert rules, and 23 recording rules. Resume with exact-head CI.

Final evidence: capability-count checker passed in the integrated GATE; exact-head CI run 33047209645 succeeded. Derived totals remain 293 metrics, 17 log events, 16 collectors, 2 dashboards, 100 alert rules, and 23 recording rules.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Generated capability counts are committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending exact-head CI.

Completion: verified by negative tests, full gate, and exact-head CI.
<!-- SECTION:FINAL_SUMMARY:END -->
