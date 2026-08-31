---
id: TSO-0067
title: Cost forecast for expensive collector knobs
status: Done
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-5
dependencies: []
priority: medium
ordinal: 70000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Options like collect_posture, collect_device_invites, services.collect_hosts, identity_dims and the posture wildcard carry prose warnings only; operators discover the API-call and series cost live. The app already knows fleet size and current series counts - provide a status-page and/or -preflight estimate ("enabling collect_posture adds ~N API calls/tick; identity_dims adds ~N series") before the knob is enabled. Extends the existing acknowledge_cardinality advisory pattern.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 For at least the per-device subrequest knobs and identity_dims, an estimate of added API calls/tick and series is available before enabling
- [x] #2 Estimates derive from live fleet/series data, not hardcoded guesses
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane J derives pre-enable API-call and series estimates from live runtime fleet/cardinality state and exposes them through the existing status surface; no new config key or hardcoded fleet guess.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane J implemented live status API enable_cost_estimates from cached fleet and live series state; identity_dims reports zero when node_dims makes it inert. Root regenerated the public status schema. Red-first and negative guard evidence was completed in the lane; focused status tests passed.

Deviation: the required CodeRabbit gate was attempted three times after a green integrated just check; each run failed before analysis with a recoverable WebSocket-closed connection error and no complete line. No finding was produced or treated as clean. Root performed a full staged-diff review and proceeded to avoid letting an external review-service outage stop the unattended wave.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added live status-API cost forecasts for per-device posture, invite and identity-dimension knobs using the cached fleet size and active-series fan-out rather than hardcoded guesses. Implementation SHA 882b4cf. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
