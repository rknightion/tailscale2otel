---
id: TSO-0139
title: Expose safe log-stream destination configuration alongside delivery health
status: Done
assignee:
  - '@codex'
created_date: '2026-09-05 17:36'
updated_date: '2026-09-05 22:06'
labels: []
dependencies: []
references:
  - spec/tailscale-api.json
  - internal/tsapi/logstream.go
priority: medium
type: feature
ordinal: 140000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The logstream collector reports delivery status but cannot distinguish the configured destination type from an absent or inaccessible sink. Discovery found the vendored getLogStreamingConfiguration GET unused while internal/tsapi/logstream.go only writes this configuration and reads delivery status. This is a distinct configuration surface, not a replacement for existing delivery counters. Expose only bounded configured state and destination type; the response can contain URLs and credentials.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A read-only configuration lookup reports bounded destination type and confirmed configured state for each supported log type without issuing writes.
- [x] #2 The documented ambiguous 404 does not become a false assertion that streaming is disabled; tests distinguish successful configuration from inaccessible or unknown state.
- [x] #3 No URL, token, credential or other unbounded destination field enters metric labels or log bodies; an allowlist test proves this.
- [x] #4 Every adopted signal has catalog documentation, a dashboard panel and derived coverage; existing delivery-health signals remain intact.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement the bounded read-only log-stream configuration client and collector surface in the frozen Lane A paths; root will integrate wiring, catalog, dashboard generation, full gates, review, release, and live rollout.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Delivered by dbaeeb4f8a130c380361d76a3fb07bb1cda1412b. Targeted 200/404/403 and allowlist tests passed; `just gen` was deterministic, `just --fmt --check` and `just check` passed. CodeRabbit completed with three findings reviewed and rejected against the vendored enum and package/runtime contracts. Exact-head CI 33993341040 attempt 1 succeeded. auto-rc 33993736047 attempt 1 published v5.0.0-rc.27. Live RC.27 status showed the logstream collector successful, both read-only configuration operations supported, and the bounded configured gauge present for both supported log types.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added a read-only per-log-type destination configuration lookup and bounded configured gauge without retaining URL or credential fields. Ambiguous 404 remains unknown, 403 remains scope_denied, delivery-health signals remain intact, generated catalog/dashboard coverage is current, CI is green, and RC.27 live evidence confirms the new surface.
<!-- SECTION:FINAL_SUMMARY:END -->
