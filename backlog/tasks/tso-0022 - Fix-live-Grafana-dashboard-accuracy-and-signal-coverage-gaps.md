---
id: TSO-0022
title: Fix live Grafana dashboard accuracy and signal coverage gaps
status: Done
assignee:
  - '@codex'
created_date: '2026-08-27 17:26'
updated_date: '2026-08-27 19:58'
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
- [x] #1 ACL revision age is either persisted across exporter restarts with explicitly approximate first-observed semantics, or every metric description, panel title/description, runbook, and alert reference is relabelled so it cannot be read as the true policy modification time; tests cover first observation, stable ETag, changed ETag, and restart behavior.
- [x] #2 Tailnet and provider dashboard variables are applied consistently to Policy and Config metric and Loki queries, including ACL, DNS, identity inventory, integrations, and mixed device/node panels; a generated-dashboard test prevents unscoped regressions.
- [x] #3 Zero-safe panels distinguish a present metric with no matching rows from an absent prerequisite. This covers unauthorized/internal devices, external/shared-in devices, UDP-blocked devices, healthy failure counters, auto-approvers, and profile-upload failures without turning genuinely unavailable telemetry into zero.
- [x] #4 Cardinality panels derive cap percentages, limits, thresholds, and wording from tailscale2otel_series_limit rather than a hard-coded 10K; the headroom table either computes remaining headroom or is renamed to utilization; descriptive totals use neutral colors.
- [x] #5 Raw Node Metrics tables show the intended node and small value/count fields without resource-label clipping, and the live node-target summary exposes an immediately readable up/total ratio or failed-target count.
- [x] #6 The NAT and relay panel no longer combines a dimensionless hard-NAT fraction with configured peer-relay endpoints under one unit or implies measured DERP pressure; all remaining queries honor dashboard selectors.
- [x] #7 Flow-log aggregate panels distinguish absent results from zero and are reconciled with the populated raw flow-log stream, including observed bandwidth and top node-pair talkers; healthy ingestion-hygiene counters render an explicit zero when their source family is present.
- [x] #8 Posture visualizations retain the posture attribute dimension or require an explicit selected attribute, do not treat arbitrary numeric zero as universal failure, and report numerator, denominator, and unknown/unsupported population where a coverage percentage would otherwise imply noncompliance.
- [x] #9 Every emitted trace span class is accounted for by a bounded Tempo discovery, table, or metrics panel and a source-to-dashboard coverage gate. At minimum account for scheduler scrape, Tailscale API, Headscale API, node-metrics scrape, stream receiver, webhook receiver, and release-check spans.
- [x] #10 The dashboard provides a meaningful route to the emitted Pyroscope profile set, such as bounded profile panels or explicit Profiles/trace-to-profile drilldown, rather than only upload-health metrics; disabled, idle, and healthy-no-failure states remain distinguishable.
- [x] #11 Inventory/configuration facts and prerequisite-missing states use neutral presentation, labelled configuration values such as external-tailnets role remain visible, and narrow tables exclude infrastructure columns that hide the operator fields.
- [x] #12 Generated artifacts and signal dispositions are regenerated; dashboard/query tests, catalog coverage gates, PromQL checks, and relevant module checks pass; the repaired dashboards are published through GitSync and every affected panel is rechecked in the authenticated live UI.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
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

Remaining campaign: map AC #2-#12 into single-owner dashboard/query/test packets; serialize generator integration; implement with focused red/green checks; regenerate; run integrated gates and CodeRabbit; publish through GitSync and recheck affected authenticated live panels before finalization.

Publication routing: deliver deploy/grafana/**/*.json only through the repository GitSync workflow; publish deploy/alerts/grafana-managed/**/*.json only with gcx resources push, then verify each surface with its own read-back.
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

Live ACL proof completed after deployment. The exact implementation revision was pulled and the service became healthy. The generated dashboard was published through its GitSync source and both the Overview summary and Security & Policy > Policy & Config > Access & ACL panel rendered the revision-first-observed fallback with no visible query error. The first restart exposed that the deployment had intentionally selected an in-memory checkpoint store because flow and audit ingestion are streamed; this reset the ACL epoch and reproduced the uptime-like defect. Switching the already-mounted state path to the file store created a checkpoint, and a second controlled restart proved persistence: a post-restart sample retained the pre-restart first-observed epoch. The authoritative audit-change metric remained absent, so no audit timestamp was fabricated. TSO-0023 tracks separating durable semantic evidence from optional poll cursors so streamed deployments cannot repeat this configuration trap.

Fan-out infrastructure note: five correctly routed Codex MAPPING lanes (Luna/medium) all failed before repository access with the same configured Responses-endpoint HTTP 404. No child edits, tracker writes, commits, deployments, or live-system actions occurred. Per the canonical route contract, no substitute route was reported as equivalent; root continued locally.

AC #2-#6 implementation progress: added a built-dashboard selector contract and fixed 38 Policy & Config Prometheus/Loki queries; added prerequisite-aware zero fallbacks for empty device populations, UDP-blocked devices, ACL auto-approvers, and profile-upload failures; replaced hard-coded 10K cardinality assumptions with tailscale2otel_series_limit utilization; exposed a Node Metrics up/total ratio and narrowed raw tables; split the mixed-unit NAT/relay panel into selector-aware hard-NAT fraction and configured peer-relay inventory panels. Focused generator tests pass; integrated generation and repository gates remain pending.

CodeRabbit organisation-plan review completed with seven findings. Fixed the two dashboard correctness findings: zero-valued series limits are filtered before utilization division with an explicit unlimited state, and posture passing numerators preserve zero only while their reporting family is present. Clarified dashboard-versus-rule publication routing in the plan. Four generated alert-link findings were checked against the title resolver and current dashboard family: the cited IDs correctly resolve to their declared unique panel titles, so no generated JSON was edited.

The exact-head GitSync dashboard job failed before publication because the destination repository had migrated from networking/ to grafana/networking/ while this workflow retained the old path. Updated the dashboard copy/prune target and added an explicit destination-directory assertion. actionlint and the internal CI workflow contract test pass. CodeRabbit was skipped for this declarative CI YAML-only repair, per policy.

Final integrated evidence: full regeneration passed; 210 dashboard generator tests and 110 alert generator tests passed; catalog disposition/coverage/dashboard gates passed; promqlcheck parsed 655 PromQL and reported zero failures while explicitly leaving 35 LogQL and 6 TraceQL expressions syntax-unparsed; root build, vet, race tests, and golangci-lint passed; every module passed build/vet/race/tidy/lint and pinned Go 1.27 govulncheck found no vulnerabilities, while the stale installed govulncheck wrapper remained a reported failure. Exact-head CI and grafana-sync both passed on e0d20a2e3ae1f8c65d2af4cc757641abc17b93c3. GitSync destination blob hashes exactly matched both local dashboards, and its commit named the exact source head. Authenticated lazy-initialized UI checks covered every repaired panel family with no visible query error; trace-class activity, CPU and heap profile activity/flamegraphs, cardinality utilization, flow aggregates, node tables/ratio, scoped policy/config panels, zero-safe populations, posture population accounting, and neutral configuration panels all rendered. Alert-rule read-back remained 123 shipped/deployed, 81 paused, zero missing/orphaned/drifted.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Repaired the collated Grafana accuracy and coverage defects, added generator/source regression gates, regenerated dashboards and alert links, fixed the migrated GitSync destination path, and published both delivery surfaces. Verified locally, on exact-head CI, against GitSync blob hashes and rule read-back, and through authenticated lazy-initialized live panel checks.
<!-- SECTION:FINAL_SUMMARY:END -->
