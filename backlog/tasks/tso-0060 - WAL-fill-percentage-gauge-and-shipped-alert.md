---
id: TSO-0060
title: WAL fill-percentage gauge and shipped alert
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:31'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-4
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
- [x] #1 Fill-percentage gauges for bytes and entries are emitted and catalogued
- [x] #2 A generated alert fires before the fail-closed threshold; artifacts regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Integration deviation: the lane added separate WAL entry-fill and byte-fill panels, but that exceeded the frozen 35-panel leaf ceiling. Root consolidated both series into one WAL capacity panel, preserving both metrics and their alert links without weakening the panel-budget guard. W1 full gate passed.

Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added bounded WAL fill-percentage telemetry and its generated paused guidance alert/panel surfaces. Verified by WAL telemetry tests, generated rule execution and drift guards, real rule push/readback, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
