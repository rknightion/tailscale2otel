---
id: TSO-0163
title: >-
  Drop an ingress snapshot after N consecutive permanent 4xx rejections and
  count the loss
status: Done
assignee:
  - '@loop23'
created_date: '2026-10-09 22:26'
updated_date: '2026-10-10 16:56'
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
- [x] #1 A fake exporter that returns a permanent 4xx N times causes the pinned collection (and, separately, log batch) to be dropped on the Nth consecutive rejection, the loss counter to increase by one per dropped unit, and the next healthy collection to deliver
- [x] #2 401, 403, 408, 429 and 5xx rejections never drop, however many occur; a success resets the consecutive count
- [x] #3 Dropped members' WAL entries complete, so the WAL drains and new appends are accepted
- [x] #4 ingress_wal.max_permanent_rejections exists with default 5, rejects values below 1, and is documented in config.example.yaml, docs/configuration.md and the Helm values; just gen leaves no diff
- [x] #5 The loss counter is catalogued, appears in docs/metrics.md, and an advisory alert rule for it is generated under deploy/alerts by build_rules.py
- [x] #6 The design doc's permanent-rejection line is superseded by a note in docs/high-availability.md or docs/configuration.md stating the new behaviour and the data-loss tradeoff
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement bounded permanent-4xx drop per frozen loop20 decision, preserve transient/auth retry behavior, complete WAL members with explicit per-signal loss accounting, wire config/status/catalog/advisory alert and regenerate owned outputs; prove regression and runtime drain, then root security review and CodeRabbit before landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Loop20 ownership packet amended before implementation: config reaches providers through internal/app/options.go, and chart instructions mandate a Chart.yaml version bump on values changes. Root grants those narrow paths plus options mapping tests; no appVersion or extra live authority. Read-only blocked inspection consumed zero implementation attempts.

Loop20 second packet amendment: public capability-count prose in README, docs/index, docs/comparison and docs/alerts, plus ingresswal config tests. Goal minimum 1 and AC4 require rejecting the new rejection-limit field below 1 unconditionally, including disabled WAL. Existing disabled storage-setting inertness remains; disabled fixture must use a valid new limit and explicitly retain old-field contract, with new disabled/enabled invalid-limit tests. This is an intended new-field contract change, not test weakening.

loop20: 3 implementation cycles (two worker cycles, root rescue); review-repair round 1. Guarded security delta PASS and final just check green on exact retained 76-file candidate; major first-loss alert defect corrected with durable missing-baseline promtool fire/quiet/clear proof. NOT LANDED: CodeRabbit runtime/config and Helm shell scopes complete with zero findings, but generator selection returns No files to review despite staged changed sources. Dashboard generator failed initial scope, fresh retry and parent-scope retry (two infrastructure retries exhausted). Missing mandatory coverage is a defect, not acceptance. Retained composed base aefe72b00e347589dff52a3ae1292abacc65bf5e, full binary patch SHA256 d8d5031d6a98e63661c1643bf3a1fcd6600784ba320fd1b5a20c79318c5af1d0. Resume by repairing review availability/selection and reviewing the exact retained generator bytes; do not reconstruct from prose or reset attempt count. Additional implementation changes require owner ceiling decision. No live rule/dashboard publication occurred.

loop21: owner waived generator review gap for unchanged retained candidate; hash verified, applied to current main, just gen exit 0 and complete binary patch unchanged. No new implementation cycles authorised.

loop21: unchanged 76-file candidate landed at c30c532923029be90e23e712fafae2812e362a4f after isolated exact-patch gate exit 0 (267.72s) and owner review waiver. Post-land composed gate in primary checkout exited 1 (70.113s): formatter selected ignored machine-local codex/loop20 Go evidence, subsequent checks not reached. D20 requires park on red gate; no source repair or unchanged rerun. Still 3 implementation cycles total, none added. Resume requires owner re-grading this environment-only gate failure and authorising clean-checkout composed proof; required exact-SHA CI is being observed separately. No SIGTERM.

loop21 exact landed SHA c30c532923029be90e23e712fafae2812e362a4f: CI run 38040463418 and Helm run 38040463708 completed success, ci-success/helm-success both success. grafana-sync run 38040463440 success for rule publication and GitSync dashboard delivery; no direct dashboard push. Release run 38040463900 completed success; release-only skipped jobs are not counted as passes. Remains Parked solely on primary-checkout composed fmt failure; no AC or DoD checked because required composed proof is red.

loop22: one fresh detached current-main 9c8646b6c1fccedf0a3bf3ca16f218467f725ea4 composed gate exit 1 after 173.873s: installed golangci-lint 2.13.2 differs from pinned v2.14.0; formatting passed, later checks not run, no SIGTERM. D24 mandates park on red, no rerun or code repair. Still 3 historical implementation cycles, zero added. Exact c30c532923029be90e23e712fafae2812e362a4f CI 38040463418 ci-success and Helm 38040463708 helm-success reverified success. Resume: owner authorize a new clean-worktree composed gate after environment provisioning; AC/DoD remain unchecked.

loop23: 3 historical implementation cycles, 0 new; D30 clean-worktree composed proof green closes previous environment-only park.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
loop23 D30: existing bounded permanent-4xx drop implementation landed at c30c532923029be90e23e712fafae2812e362a4f; exact-SHA CI 38040463418 ci-success and 38040463708 helm-success reverified successful. Fresh detached current-main c9ef70b7d79d65981156d5a6f5b96204d7e07775 setup-first composed just check passed exit 0 (615.582s), including generation gates; just --fmt --check exit 0. Unchanged clean tree, no signals or retries. All AC and DoD finalized per D30; no new source work.
<!-- SECTION:FINAL_SUMMARY:END -->
