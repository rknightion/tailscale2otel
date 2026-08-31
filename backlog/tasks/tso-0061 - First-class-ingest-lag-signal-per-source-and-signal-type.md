---
id: TSO-0061
title: First-class ingest-lag signal per source and signal type
status: In Progress
assignee: []
created_date: '2026-08-30 09:31'
updated_date: '2026-08-31 00:37'
labels: []
milestone: m-4
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Read internal/catalog/signal_dispositions.json for tailscale2otel.ingest.event_age and .capture_delay. If both are already visualized, close the task on evidence with the metric names and dispositions and stop.
2. Otherwise add the panel(s) to the health dashboard Ingestion tab (deploy/grafana/gen/tabs/health_ingestion.py), broken down by source and signal.
3. Record the measured p95 finding and, if the 5.6h flow figure survives scrutiny, file it as its own task rather than folding an investigation into this one.

Lane F first verifies whether the existing ingest_event_age histogram is already visualized; if so, close on evidence and scrutinize the observed 5.6-hour stream lag, filing surviving lag as needs-triage work. No duplicate signal is created.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
PRE-WAVE-3 RESEARCH, 2026-08-30 — AC#1 is ANSWERED, and the answer is PRESENT, not absent. The task body says the sweep could not find an exported lag signal. It exists.

Live on the lab stack (gcx metrics query against the m7kni stack, 2026-08-30):
- tailscale2otel_ingest_event_age_seconds_{bucket,count,sum} — a HISTOGRAM, already labelled by source and signal. This is exactly AcceptedAt minus EventTime.
- tailscale2otel_ingest_capture_delay_seconds_{bucket,count,sum} — the companion capture-to-accept delay.
- tailscale2otel_ingest_last_event_timestamp_seconds, _records_total, _size_bytes_total, _timestamp_skew_total round out the family.

So the build half of AC#2 is already done and the task reduces to the "if present, just surface it on the dashboard" branch: put it on the health dashboard Ingestion tab. Verify against internal/catalog/signal_dispositions.json which disposition these carry before assuming a panel is missing — if they are already visualized there is nothing to do at all and the task closes on evidence.

Load-bearing observation while measuring: p95 of ingest_event_age for signal=flow source=stream on the lab deployment is ~20200 SECONDS (5.6 hours). Either flow events genuinely arrive that late on the streaming path, or the histogram is being skewed by a catch-up. Whichever it is, that number is the reason this signal is worth a panel — do not just add the panel, look at what it says.

Evidence close: the existing tailscale2otel.ingest.event.age histogram is labelled by source and signal, its capture-delay companion exists, and both are already visualized together on the Health/Ingestion freshness panel. No duplicate signal or panel will be added. The previously measured high stream-flow p95 was not re-queried in this lane; TSO-0093 now tracks a fresh causal investigation.
<!-- SECTION:NOTES:END -->
