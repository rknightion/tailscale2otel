---
id: TSO-0061
title: First-class ingest-lag signal per source and signal type
status: To Do
assignee: []
created_date: '2026-08-30 09:31'
labels: []
dependencies: []
priority: medium
ordinal: 64000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/ingest.AcceptedEvent carries EventTime/AcceptedAt - the raw material for "is streaming keeping up" - but it is unconfirmed whether an exported lag gauge/histogram exists (check internal/app/selfobs.go and heartbeat.go first; the improvement sweep could not find one). If absent, export AcceptedAt minus EventTime as a histogram per source (stream/webhook/poll) and signal type, catalogue it, and surface it on the health dashboard Ingestion tab. If present, just surface it on the dashboard.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Verified whether the signal already exists; result recorded
- [ ] #2 An ingest-lag histogram per source is exported and visualized
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
