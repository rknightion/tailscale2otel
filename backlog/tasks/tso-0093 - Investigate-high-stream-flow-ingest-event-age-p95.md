---
id: TSO-0093
title: Investigate high stream flow ingest-event-age p95
status: To Do
assignee: []
created_date: '2026-08-31 00:37'
updated_date: '2026-09-01 20:02'
labels: []
milestone: m-10
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
- [x] #1 Fresh stream flow records reproduce or disprove the elevated p95 with timestamped evidence
- [ ] #2 Event age is correlated with capture delay and accepted time to distinguish delivery lag from retry, backfill, and metric-semantics causes
- [x] #3 The cause is classified and any remediation is captured as an implementation-ready follow-up
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Re-measure stream-flow ingest event age after the Wave 3 rollout. 2. Correlate event age with capture delay and accepted time in the same window. 3. Classify the cause and create an implementation-ready follow-up only if remediation is warranted.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Live re-measurement on the Wave 3 build over 2026-09-01 14:30Z-20:30Z disproved the prior sustained ~20,200s p95. The 15-minute stream/flow event-age p95 was normally about 280-287s. Two isolated samples rose to 2,158.8s at 16:25Z and 1,702.7s at 16:30Z. At those exact samples accepted throughput remained 3.10 and 3.08 records/s and newest-accepted-event freshness remained 122.08s, so neither a receiver stall nor broad delivery lag occurred; the histogram was seeing a minority of older/backfilled records while current records continued arriving. The six-hour accepted rate ranged up to 4.25 records/s and newest-event freshness stayed below 183s.

Capture-delay correlation is not available for stream/flow: the event-age, accepted-record, and newest-event series are present, but the capture-delay histogram is absent for stream/flow while it is present for stream/audit. This prevents separating publisher capture lag from post-capture delivery/retry for the old-record minority. TSO-0106 tracks restoration and is the concrete resume boundary. Classification: intermittent late/backfilled acceptance, not metric timestamp regression and not a sustained stream outage; exact upstream-vs-delivery location remains unproven until TSO-0106 lands.

Wave 5: unparked. TSO-0106 restores stream/flow capture-delay telemetry inside this same wave, so the resume boundary is now reachable. Resume by repeating the same-window correlation once 0106 has landed and its telemetry is live, then classify whether the old-record minority is upstream capture lag or post-capture delivery. Acceptance criterion 2 is the only one still open.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Fresh live data disproved a sustained 20,200-second p95 and classified the observed spikes as intermittent old-record acceptance while current traffic stayed fresh. Parked at the concrete boundary: restore stream/flow capture-delay telemetry in TSO-0106, then repeat the same-window correlation to distinguish upstream capture from post-capture delivery.
<!-- SECTION:FINAL_SUMMARY:END -->
