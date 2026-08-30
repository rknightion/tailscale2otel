---
id: TSO-0060
title: WAL fill-percentage gauge and shipped alert
status: To Do
assignee: []
created_date: '2026-08-30 09:31'
labels: []
dependencies: []
priority: medium
ordinal: 63000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
ingresswal Health reports absolute PendingBytes/PendingEntries; a full WAL fails requests closed with 503 but nothing warns at 80% full. Derive percent-of-capacity self-obs gauges from the configured limits, catalogue them, surface on the health dashboard Ingestion tab, and ship a ts2o alert rule (warning threshold before the cliff) in deploy/alerts/gen.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Fill-percentage gauges for bytes and entries are emitted and catalogued
- [ ] #2 A generated alert fires before the fail-closed threshold; artifacts regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
