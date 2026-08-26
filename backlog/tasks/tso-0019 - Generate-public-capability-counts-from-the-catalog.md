---
id: TSO-0019
title: Generate public capability counts from the catalog
status: To Do
assignee: []
created_date: '2026-08-26 11:02'
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
- [ ] #1 A deterministic source provides the public metric, log-event, collector, dashboard, alert-rule, and recording-rule counts
- [ ] #2 README, docs index, Comparison, FAQ, and navigation summaries cannot drift silently from those sources
- [ ] #3 The check fails with an actionable regeneration or edit instruction
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
