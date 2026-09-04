---
id: TSO-0131
title: >-
  Emit a coordination handover counter so leadership flapping can be alerted
  truthfully
status: Done
assignee:
  - '@codex'
created_date: '2026-09-04 07:30'
updated_date: '2026-09-04 12:28'
labels:
  - needs-triage
dependencies:
  - TSO-0130
priority: medium
type: feature
ordinal: 132000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 10 rejected a leadership-flapping alert rule and was right to. The only coordination signal is tailscale2otel.coordination.leader, a last-value gauge, and changes() over a retained gauge series does not count completed handovers: it counts sample transitions in whatever the series retained, which conflates a real handover with a scrape gap, a restart, or a series appearing and disappearing.

Flapping is worth alerting on because its likeliest cause is mundane and fixable: a renew_deadline too tight for the API server's observed latency, rather than any real failure. TSO-0129 now rejects timings client-go cannot run at all, but a configuration that is legal and still marginal produces exactly this.

Owner decision 2026-09-04: add the counter, and derive it from the same Lease observation TSO-0130 builds for self-fencing rather than adding a second watcher.

Scope note. A new signal is not free in this repository: it needs a descriptor in the catalog, a docs/metrics.md regeneration, a dashboard panel to satisfy the visualized disposition, and a row in the coverage manifest. The manifest derives every disposition from the real artifacts, so the panel is what settles it and there is no value a human may assign.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A monotonic counter records completed leadership handovers, incremented from the Lease observation TSO-0130 introduces rather than a second watcher
- [x] #2 The counter distinguishes a handover from a process restart, so a rolling deployment does not read as flapping
- [x] #3 A flapping alert rule is generated from the counter, ships advisory and non-paging like the three Wave 10 rules, and is proved by a real gcx resources push
- [x] #4 The signal has a dashboard panel and its coverage-manifest row is derived, not hand-assigned
- [x] #5 The rule's threshold is justified against a measured or reasoned handover rate, not picked round
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. After TSO-0130 exposes its watcher seam, add a monotonic completed-handover counter from that same observation stream and test first that restarts do not increment it.
2. Add the catalog descriptor, metric documentation, and a dashboard panel that derives the signal disposition.
3. Generate an advisory non-paging flapping rule from a justified measured or reasoned threshold, execute its Prometheus fixtures, and regenerate shared catalog artifacts once at integration.
4. Prove rule deployability with an explicit m7kni-context push and verify-deploy read-back.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Live evaluator history on the exact-head prerelease: after the isolated sibling was switched to OTLP-only process telemetry with every collector disabled, a live Mimir query returned a coordination leader sum of 1 and a completed-handover counter sum of 1. The first Grafana runtime snapshot preceded the first export and reported Normal (NoData); after one complete 60 s evaluation interval, CoordinationFlapping reported the sibling Normal, evaluator health ok, and no last error, pending state, or firing state. The single completed handover correctly remained below the >2 in 15m threshold.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Completed in 84776837 with additive replay correction fe3c3cc6. The shared Lease watcher emits the monotonic coordination handover counter only for completed incoming handovers; initial state and process restart remain zero. The generated advisory non-paging rule uses sum(increase over 15m) greater than 2 for 5m: one controlled replacement plus one recovery remains quiet, while a third completed handover is churn. The signal has a generated dashboard panel and derived coverage row. A real gcx push using context m7kni accepted 135 resources with zero errors, and just verify-deploy m7kni reported 134 shipped and deployed with zero drift. Live evaluation observed counter sum 1 and the rule Normal after a complete interval. just check, just gen, and just --fmt --check pass; exact-head CI 33870727006 succeeded on attempt 1.
<!-- SECTION:FINAL_SUMMARY:END -->
