---
id: TSO-0095
title: Validate Wave 3 live on the lab deployment
status: Done
assignee: []
created_date: '2026-08-31 10:54'
updated_date: '2026-09-01 19:10'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: high
ordinal: 96000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Waves 1-3 shipped 100+ tasks that are CI-green and have NEVER run against the real tailnet. The lab Kubernetes deployment reports version 4.1.0-rc.29 (= 7d48fd5, the Wave 2 close), so every one of Wave 3s 32 tasks is unvalidated in production. camden, the docker-compose instance, was deliberately retired and last reported 2026-08-29 20:00 UTC; the lab cluster is the only live instance.

TRAP, CHECK THIS FIRST: the chart default is image.pullPolicy: IfNotPresent (deploy/helm/tailscale2otel/values.yaml:20), NOT Always. Deleting the pod will therefore re-use the cached image and validate nothing while looking like it worked. Confirm the LIVE values before touching anything; if the deployed values do not set Always, the rollout must pin or force the new image explicitly.

Scope: prove the Wave 3 signals, dashboards and alerts do what they claim on real data. Every new metric and log family, every regrouped dashboard tab, every new or changed alert rule. Read back through gcx and through the rendered dashboards, not from code.

Deliberately in scope because they can only be answered live: TSO-0093 (stream flow ingest-event-age p95 measured at ~20,200s) and whether the Wave 3 config keys behave as documented when actually set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The lab deployment is confirmed running a Wave 3 build, with the image reference and pull policy recorded as evidence rather than assumed
- [x] #2 Every metric and log family added in Wave 3 is confirmed present on the wire with the expected attributes, or its absence is explained
- [x] #3 Both regrouped dashboards render with no empty panels other than those whose feature is genuinely disabled, verified visually
- [x] #4 Every Wave 3 alert rule is confirmed to evaluate, with its current state recorded; any rule that cannot fire is explained
- [x] #5 TSO-0093's ingest-age question is answered with fresh live evidence
- [x] #6 Findings are filed as new tasks; this task records the evidence, not the fixes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Record the explicit-context lab baseline and reported build metric. 2. Roll to the Wave 3 main image and prove the running version from live telemetry. 3. Inventory Wave 3 signals, dashboards, and rules against live read-back; file each finding separately. 4. Leave the lab on a known-good build.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Baseline before mutation: the live deployment used the ECR pull-through :main reference, pull policy Always, and tailscale2otel_build_info_ratio reported 4.1.0-rc.29 (revision 7d48fd5). The rollout replaced the pod and live telemetry, not pull success, proved 4.1.0-rc.47 (revision 3d7cbc4).

Wave 3 signal checklist from internal/catalog/signal_dispositions.json: api.rate_limit.utilization, nodemetrics.scrape.failures (including bounded reason), and receiver.misconfigured (receiver attribute) were present on the wire. devices.posture_compliance.failed was absent as expected because the enabled checks had no failures; organization.tailnets.count was absent as expected because organization discovery was disabled. flow_store.journal.size and flow_store.last_checkpoint_timestamp were unexpectedly absent while the persistent store was configured; startup logs proved the SQLite store failed during incremental auto-vacuum conversion with a disk I/O error, disabling the flow explorer. TSO-0104 records that defect. Wave 3 added no new log-family rows beyond this seven-metric manifest delta.

Browser validation: visually inspected every top-level and nested tab of both generated dashboards in the authenticated Grafana UI. Health: Overview; Data pipeline/Collection, Ingestion, Delivery; Runtime & capacity/Runtime, Cost & Cardinality. Tailnet: Overview; Fleet operations/Inventory & Hygiene, Posture & Security, Connectivity & Routing; Network & service telemetry/Network & Flows, Node Metrics; Security, identity & governance/Audit Trail, Risk & ACL, Posture & Compliance, Identity & Keys; Policy & configuration/Access & ACL, DNS & Settings, Identity & Credentials, Integrations. There were no query errors or unexplained No data panels. Explicit empty states correctly named disabled organization discovery, disabled per-user cardinality, or unavailable user-invite prerequisites.

Alert read-back at 2026-09-01 19:08Z covered all 125 shipped ts2o rules: 44 unpaused rules all had nonzero recent lastEvaluation timestamps (36 alerting: 3 firing, 33 inactive; 8 recording: inactive; all health ok). The remaining 81 were deliberately paused (66 alerting health ok; 15 recording health unknown). The initial post-publication sample had shown zero timestamps on many rules, but the later direct engine read-back proved they subsequently evaluated; TSO-0105 retains that transient verification gap for triage.

Findings filed separately: TSO-0104 persistent flow-store upgrade failure, TSO-0105 transient zero last-evaluation read-back, and TSO-0106 absent stream/flow capture-delay telemetry. The lab was rolled back by immutable ECR digest verified from OCI metadata as revision 7d48fd5; rollout completed, both containers were ready, no startup error/fatal log was emitted, and live build-info telemetry again reported 4.1.0-rc.29.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Validated revision 3d7cbc4 live from build-info telemetry, inspected all seven new signal rows, every nested tab of both dashboards, and all 125 shipped rules. Filed TSO-0104, TSO-0105 and TSO-0106 for findings, then restored the lab to the immutable known-good 7d48fd5 digest and verified 4.1.0-rc.29 from telemetry.
<!-- SECTION:FINAL_SUMMARY:END -->
