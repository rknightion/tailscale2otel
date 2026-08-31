---
id: TSO-0093
title: Investigate high stream flow ingest-event-age p95
status: To Do
assignee: []
created_date: '2026-08-31 00:37'
labels:
  - needs-triage
dependencies: []
priority: medium
type: spike
ordinal: 94000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A lab observation recorded tailscale2otel_ingest_event_age_seconds for stream flow events at approximately 20,200 seconds p95. Establish whether this reflects delayed upstream delivery, retry or backfill catch-up, or timestamp and histogram semantics before proposing remediation. The observation came from TSO-0061 research and has not been independently re-queried during Wave 3.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Fresh stream flow records reproduce or disprove the elevated p95 with timestamped evidence
- [ ] #2 Event age is correlated with capture delay and accepted time to distinguish delivery lag from retry, backfill, and metric-semantics causes
- [ ] #3 The cause is classified and any remediation is captured as an implementation-ready follow-up
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
