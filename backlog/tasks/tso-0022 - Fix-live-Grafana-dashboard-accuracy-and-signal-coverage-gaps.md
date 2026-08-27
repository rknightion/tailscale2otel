---
id: TSO-0022
title: Fix live Grafana dashboard accuracy and signal coverage gaps
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-27 17:26'
updated_date: '2026-08-27 18:10'
labels:
  - needs-triage
dependencies: []
priority: high
type: bug
ordinal: 25000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A 2026-08-27 authenticated six-hour live audit rendered 407 panels across the Tailnet and Exporter Health dashboards. No Grafana query-error banners were present, but the rendered data and generated source proved several misleading zero/no-data states, selector omissions, semantic labels, table layouts, and signal-coverage gaps.

The ACL age is the clearest correctness defect: the upstream API exposes no modification timestamp, so the collector stamps the first observation of the current ETag. The live value matched the exporter process start after a restart, not a policy edit.

Keep this one task as the collated repair unit requested by the operator. Do not put lab identifiers, device names, addresses, account IDs, or raw telemetry values in tracked notes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 ACL revision age is either persisted across exporter restarts with explicitly approximate first-observed semantics, or every metric description, panel title/description, runbook, and alert reference is relabelled so it cannot be read as the true policy modification time; tests cover first observation, stable ETag, changed ETag, and restart behavior.
- [ ] #2 Tailnet and provider dashboard variables are applied consistently to Policy and Config metric and Loki queries, including ACL, DNS, identity inventory, integrations, and mixed device/node panels; a generated-dashboard test prevents unscoped regressions.
- [ ] #3 Zero-safe panels distinguish a present metric with no matching rows from an absent prerequisite. This covers unauthorized/internal devices, external/shared-in devices, UDP-blocked devices, healthy failure counters, auto-approvers, and profile-upload failures without turning genuinely unavailable telemetry into zero.
- [ ] #4 Cardinality panels derive cap percentages, limits, thresholds, and wording from tailscale2otel_series_limit rather than a hard-coded 10K; the headroom table either computes remaining headroom or is renamed to utilization; descriptive totals use neutral colors.
- [ ] #5 Raw Node Metrics tables show the intended node and small value/count fields without resource-label clipping, and the live node-target summary exposes an immediately readable up/total ratio or failed-target count.
- [ ] #6 The NAT and relay panel no longer combines a dimensionless hard-NAT fraction with configured peer-relay endpoints under one unit or implies measured DERP pressure; all remaining queries honor dashboard selectors.
- [ ] #7 Flow-log aggregate panels distinguish absent results from zero and are reconciled with the populated raw flow-log stream, including observed bandwidth and top node-pair talkers; healthy ingestion-hygiene counters render an explicit zero when their source family is present.
- [ ] #8 Posture visualizations retain the posture attribute dimension or require an explicit selected attribute, do not treat arbitrary numeric zero as universal failure, and report numerator, denominator, and unknown/unsupported population where a coverage percentage would otherwise imply noncompliance.
- [ ] #9 Every emitted trace span class is accounted for by a bounded Tempo discovery, table, or metrics panel and a source-to-dashboard coverage gate. At minimum account for scheduler scrape, Tailscale API, Headscale API, node-metrics scrape, stream receiver, webhook receiver, and release-check spans.
- [ ] #10 The dashboard provides a meaningful route to the emitted Pyroscope profile set, such as bounded profile panels or explicit Profiles/trace-to-profile drilldown, rather than only upload-health metrics; disabled, idle, and healthy-no-failure states remain distinguishable.
- [ ] #11 Inventory/configuration facts and prerequisite-missing states use neutral presentation, labelled configuration values such as external-tailnets role remain visible, and narrow tables exclude infrastructure columns that hide the operator fields.
- [ ] #12 Generated artifacts and signal dispositions are regenerated; dashboard/query tests, catalog coverage gates, PromQL checks, and relevant module checks pass; the repaired dashboards are published through GitSync and every affected panel is rechecked in the authenticated live UI.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
ACL correctness slice:
1. Add failing tests for persisted ACL revision observation, authoritative audit-change persistence/non-regression, multi-tailnet checkpoint isolation, and timestamp provenance/dashboard wording.
2. Persist the current ETag revision first-observed timestamp through the existing checkpoint store without claiming a true modification time.
3. Persist the source timestamp of classified ACL audit events and have the ACL collector re-emit it after restarts; delayed/backfilled events must not regress it.
4. Update scoped dashboard panels to prefer audit evidence and visibly identify the approximate revision-observation fallback; regenerate generated artifacts and signal dispositions.
5. Run targeted, generated-artifact, PromQL, full Go/lint, review, GitSync/deployment, and authenticated live-panel checks.

Plan review: do not fabricate an upstream time, do not collapse missing audit history to zero, namespace state per tailnet, retain approximate wording when file persistence is unavailable, and keep persistence failures observable without suppressing other ACL telemetry.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Audit evidence (2026-08-27):

- Browser-rendered panel counts: Tailnet Overview 23; Devices leaves 62; Network and Node Metrics 65; Security and Audit 58; Policy and Config 52; Exporter Health 147. Total rendered: 407. No Grafana query-error banner appeared.
- The conditional Kubernetes Audit leaf did not render because its collector is disabled in the live deployment; this is expected, not a pass of its 32 source-defined panels. Other sentinel-gated fault rows that did not render remain source/test coverage only.
- ACL correctness proof: the live displayed age tracked the exporter process start after a restart. Source and generated metrics documentation confirm that the collector records now on the first observed ETag and stores the state only in memory; the API supplies no true modification timestamp.
- Metrics/log coverage gate passed: 201 operational metrics and 92 self-observability metrics are panelled. Sixteen concrete log-event names are panelled; the templated webhook event family is the structural exception and is reachable through the wildcard picker.
- Trace/profile coverage is outside that manifest. Live Tempo panels cover scheduler scrape spans, Tailscale API endpoint latency, and stream receiver batches, but not node-metrics HTTP scrape, webhook receiver, Headscale API, or release-check span classes. Live profiling upload is healthy, yet the dashboard has no Pyroscope query/flamegraph surface.
- Confirmed live panel defects include: absent-population panels misreporting missing collectors; UDP zero misreporting missing connectivity data; a mixed-unit NAT/relay panel; a configured 100K series limit shown through hard-coded 10K panels; utilization labelled headroom; raw Node Metrics tables whose useful values are clipped behind resource columns; ACL/DNS/policy queries ignoring tailnet/provider filters; neutral facts and unavailable states coloured as failures or health; and a posture distribution that drops the attribute dimension.
- Raw flow and audit-log panels were rechecked after lazy viewport initialization and do contain data. The Flow log stream is populated, while Observed tailnet bandwidth and Top node-pair talkers return genuine empty aggregate results without query errors; those two require query/pipeline reconciliation.
- Live operational facts to preserve as aggregates only: node-metrics discovery succeeds but 8 of 33 discovered targets are reachable; the Kubernetes-audit source is disabled; device-invite collection is enabled but the six-hour log view is empty.
- Rule deployment read-back: 123 shipped and 123 deployed, 81 paused on each side, with 0 missing, orphaned, or drifted rules.
- Validation seen: `go test ./internal/catalog -run TestSignalDispositionsInSync|TestSignalCoverageDocInSync|TestDashboard` passed. `python3 scripts/verify_deployment.py` returned deployed rule parity. Browser proof, source proof, live-host proof, and deployment proof are distinct; none is substituted for another.

ACL correctness implementation completed locally:
- Persisted current ETag revision first-observed time in the existing tailnet-namespaced checkpoint store; file-backed restart, stable ETag, changed ETag, repeated restart, absent audit history, and namespace isolation are covered.
- Persisted the newest classified ACL audit event source timestamp without allowing delayed/backfilled events to regress it; the ACL collector re-emits it after restart.
- Dashboard panels prefer the authoritative audit timestamp and visibly label the approximate first-observed fallback; both selectors now carry tailnet/provider filters. Alert and runbook references follow the renamed canonical panel.
- Added an absolute-timestamp provenance contract covering source, persisted observation, and process-local timestamps.
- Generated dashboards, rules, metrics docs, signal coverage/dispositions, and capability counts regenerated.
- Evidence: 191 dashboard generator tests passed; 651 PromQL expressions parsed with zero failures; root build/vet/race/lint passed; every module passed build/vet/race/tidy/lint and the pinned Go 1.27-compiled govulncheck found no vulnerabilities. The installed govulncheck binary itself is stale (built with Go 1.26), so its verifier leg failed before scanning and was superseded by the successful pinned go-run invocation. CodeRabbit organisation-plan review completed with zero findings.
<!-- SECTION:NOTES:END -->
