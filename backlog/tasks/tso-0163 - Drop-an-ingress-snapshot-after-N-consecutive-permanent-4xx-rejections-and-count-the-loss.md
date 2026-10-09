---
id: TSO-0163
title: >-
  Drop an ingress snapshot after N consecutive permanent 4xx rejections and
  count the loss
status: In Progress
assignee:
  - '@loop20'
created_date: '2026-10-09 22:26'
updated_date: '2026-10-09 23:16'
labels: []
dependencies:
  - TSO-0162
references:
  - internal/telemetry/scheduled_metrics.go
  - internal/telemetry/ingress_delivery.go
  - internal/telemetry/export_ack.go
priority: high
type: bug
ordinal: 163000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A metrics collection or log batch that the backend rejects permanently (for example timestamps too old after a long outage) stays pinned at the head of the scheduled reader and is retried forever. It blocks that tailnet's metrics and its ingress WAL until a restart. Flagged by loop19's security review and CodeRabbit. Rob decided on 2026-10-09 to reverse the frozen TSO-0148 design stance (codex/loop17/tso-0148-design.md: 'permanent rejection is retained, never an automatic drop of required data'). The pinned work is dropped after N consecutive permanent 4xx rejections, and the loss is made explicit. Decided details: permanent means any 4xx except 401, 403, 408 and 429 (credential and transient classes keep retrying); N is the new config key ingress_wal.max_permanent_rejections, default 5, validated at least 1; on drop, the member WAL entries complete so the WAL drains (they are not poisoned to disk); one new monotonic loss counter, labelled by signal (metrics or logs) plus the standard tailnet attributes, is shipped with an advisory (page=false) Grafana-managed alert on any increase. Logs and metrics are both in scope.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A fake exporter that returns a permanent 4xx N times causes the pinned collection (and, separately, log batch) to be dropped on the Nth consecutive rejection, the loss counter to increase by one per dropped unit, and the next healthy collection to deliver
- [ ] #2 401, 403, 408, 429 and 5xx rejections never drop, however many occur; a success resets the consecutive count
- [ ] #3 Dropped members' WAL entries complete, so the WAL drains and new appends are accepted
- [ ] #4 ingress_wal.max_permanent_rejections exists with default 5, rejects values below 1, and is documented in config.example.yaml, docs/configuration.md and the Helm values; just gen leaves no diff
- [ ] #5 The loss counter is catalogued, appears in docs/metrics.md, and an advisory alert rule for it is generated under deploy/alerts by build_rules.py
- [ ] #6 The design doc's permanent-rejection line is superseded by a note in docs/high-availability.md or docs/configuration.md stating the new behaviour and the data-loss tradeoff
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement bounded permanent-4xx drop per frozen loop20 decision, preserve transient/auth retry behavior, complete WAL members with explicit per-signal loss accounting, wire config/status/catalog/advisory alert and regenerate owned outputs; prove regression and runtime drain, then root security review and CodeRabbit before landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Loop20 ownership packet amended before implementation: config reaches providers through internal/app/options.go, and chart instructions mandate a Chart.yaml version bump on values changes. Root grants those narrow paths plus options mapping tests; no appVersion or extra live authority. Read-only blocked inspection consumed zero implementation attempts.

Loop20 second packet amendment: public capability-count prose in README, docs/index, docs/comparison and docs/alerts, plus ingresswal config tests. Goal minimum 1 and AC4 require rejecting the new rejection-limit field below 1 unconditionally, including disabled WAL. Existing disabled storage-setting inertness remains; disabled fixture must use a valid new limit and explicitly retain old-field contract, with new disabled/enabled invalid-limit tests. This is an intended new-field contract change, not test weakening.
<!-- SECTION:NOTES:END -->
