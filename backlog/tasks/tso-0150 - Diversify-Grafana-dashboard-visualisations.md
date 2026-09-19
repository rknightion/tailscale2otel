---
id: TSO-0150
title: Diversify Grafana dashboard visualisations
status: Done
assignee:
  - '@codex'
created_date: '2026-09-19 09:52'
updated_date: '2026-09-19 19:57'
labels: []
dependencies: []
references:
  - 'https://grafana.m7kni.com/d/cloudflare-network-flow/cloudflare-network-flow'
  - >-
    https://grafana.com/docs/grafana-cloud/observe-and-act/monitor-infrastructure/integrations/integration-reference/integration-ktranslate-netflow/#dashboards
  - >-
    https://github.com/grafana/jsonnet-libs/blob/master/netflow-mixin/dashboards_out/netflow-overview.json
  - 'https://grafana.com/grafana/plugins/search/?type=datasource%2Cpanel'
documentation:
  - >-
    backlog/docs/specifications/doc-0006 -
    TSO-0150-dashboard-visualisation-redesign.md
priority: high
type: feature
ordinal: 151000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The generated dashboard family relies too heavily on conventional time series, stats, and tables, which hides relationship-heavy use cases such as network flow paths. The dashboard should use a broader but disciplined visual vocabulary, informed by the live m7kni Cloudflare flow dashboard, Grafana's NetFlow integration dashboard, and Grafana Cloud's installable panel catalogue, while remaining portable when optional plugins are absent.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Both full-flow and rollup flow modes provide useful relationship visualisations and continue to respect their existing conditional rendering gates
- [x] #2 Every dashboard tab is audited and any added non-default panel type has a documented analytical purpose rather than visual novelty
- [x] #3 Optional plugin panels fail harmlessly when unavailable and identify the exact plugin to install
- [x] #4 Generated Grafana v2 artifacts reproduce deterministically and all dashboard generator contract tests pass
- [x] #5 The edited dashboards are visually verified on the m7kni stack after GitSync deployment
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
# Dashboard visualisation diversity implementation plan

**Goal:** Add purpose-built relationship, composition, geography, state, distribution, and cardinality views without weakening native fallbacks, feature gates, PII gates, or dashboard portability.

**Architecture:** Extend the schema-v2 builder with deterministic option helpers and ordered transformations. Each domain tab owns its queries and layout; optional plugins remain additive panels beside native evidence. All artifacts continue to build in one process and publish only through GitSync.

**Tech stack:** Python 3 standard library and unittest, Grafana dashboard schema v2, PromQL, Loki, Grafana core panels, `netsage-sankey-panel` 1.1.4, `marcusolsson-treemap-panel` 2.1.1, just.

**Spec:** `backlog/docs/specifications/doc-0006 - TSO-0150-dashboard-visualisation-redesign.md`

## Global constraints

- Grafana 13+ and dashboard schema `dashboard.grafana.app/v2` only.
- Do not push dashboards with `gcx` or the Grafana API; deploy through `.github/workflows/grafana-sync.yml`.
- Optional plugin panels are additive; native evidence remains available when a plugin is missing.
- Preserve all existing signal-presence and PII conditional-rendering gates.
- Bound relationship queries with `$topn` or existing cardinality controls.
- Do not change telemetry emission, cardinality controls, or PII policy.
- Generated artifacts are edited only through `deploy/grafana/gen/` and `just gen-dashboards`.

## Review focus

- Empty or absent series: every added panel keeps an accurate no-data state and does not manufacture zero.
- Missing optional plugin: only the specialist panel errors, while its row title identifies the plugin and native evidence remains usable.
- Redacted identity: Sankey and state-timeline rows disappear under the same PII gates as their source dimensions.
- Raw versus rollup: no expression or stacked panel combines the two byte families and double-counts traffic.
- High cardinality: Sankey and treemap inputs are top-N or pre-bounded, and no new dashboard-wide variable is added.

### Task 1: Builder contracts for specialist panels

**Files:** Modify `deploy/grafana/gen/builder.py`; create `deploy/grafana/gen/test_specialist_panels.py`.

**Interfaces:** Extend `organize(exclude=None, rename=None, index=None)` so `index` becomes `indexByName`. Add pure option helpers `pie_opts()`, `geomap_opts(location_field="lookup")`, `state_timeline_opts()`, `heatmap_opts()`, `sankey_opts(value_field="Value")`, and `treemap_opts(label_field, size_field, group_field=None)`. Continue using `panel(..., ptype, version=...)` for core and plugin groups.

- [ ] Write failing unittest cases that assert exact option dictionaries, ordered transformation output, custom plugin group/version preservation, and unchanged legacy `organize()` output.
- [ ] Run `python3 -m unittest deploy/grafana/gen/test_specialist_panels.py -v`; expect failures because the helpers and `index` parameter do not exist.
- [ ] Implement the helpers as static dictionary builders and add `index` to `organize()` without changing existing callers.
- [ ] Re-run the focused test and `python3 -m unittest discover -s deploy/grafana/gen -p "test_*.py"`; expect all tests to pass.
- [ ] Commit only the builder and focused test as `feat(dashboards): add specialist panel primitives`.

### Task 2: Flow topology and composition

**Files:** Modify `deploy/grafana/gen/tabs/network.py`; modify `deploy/grafana/gen/test_network_diagnostics.py`; generated `deploy/grafana/tailscale2otel-tailnet.json` is updated later in Task 5.

**Interfaces:** Add helper `topology_sankey(title, metric, prerequisite)` returning a panel name. Its instant table query is `topk($topn, sum by (tailscale_src_node, tailscale_dst_node) (rate(<metric><scope-and-flow-filters>[$__rate_interval])))`; organize fields in `Source`, `Destination`, `Bytes/s` order and set Sankey `valueField` to `Bytes/s`.

- [ ] Add failing built-artifact tests for `Traffic topology - ROLLUP`, `Traffic topology - RAW`, `Current flow share by transport`, and `Current flow share by traffic type`. Assert panel groups, top-N bounds, ordered fields, plugin version `1.1.4`, catalogue URL, and no raw/rollup mixing.
- [ ] Add failing layout tests that find the rollup Sankey row under `has_rollup_flow` plus `pii_node`, and the collapsed raw Sankey row under `has_raw_flow` plus `pii_node`.
- [ ] Run the new focused tests; expect missing-panel failures.
- [ ] Implement the two Sankey panels and two native pie panels. Keep the existing time series, top-talker bars, topology tables, and Loki stream unchanged.
- [ ] Run `python3 -m unittest deploy/grafana/gen/test_network_diagnostics.py -v`; expect pass.
- [ ] Commit the flow generator and tests as `feat(dashboards): add flow topology views`.

### Task 3: Fleet, Kubernetes, and service relationship views

**Files:** Modify `deploy/grafana/gen/tabs/devices_inventory.py`, `deploy/grafana/gen/tabs/security_compliance.py`, `deploy/grafana/gen/tabs/k8saudit.py`, `deploy/grafana/gen/tabs/policy_integrations.py`; extend `deploy/grafana/gen/test_specialist_panels.py` and relevant existing fleet/policy tests.

**Interfaces:** `Devices by country` becomes core `geomap` with `geo_country_iso_code` renamed and ordered as `lookup`, then `Devices`. `Device online state by node` uses the per-device online gauge in a `pii_perdevice` row. `Kubernetes request paths` orders user, verb, resource, Value and is gated by `has_k8s_audit` plus `pii_emails`. `VIP service topology` orders service display name, host name, Value and is gated by `has_svc` plus `pii_perdevice`.

- [ ] Write failing tests for the geomap lookup contract, device state-timeline PII gate, exact pie conversions (`Devices by OS`, `Compliance distribution`), Kubernetes top-N Sankey query and gate, and VIP-service Sankey query and gate.
- [ ] Run the focused specialist, fleet-signal, policy-inventory, and Kubernetes dashboard tests; expect failures only for missing or old panel kinds.
- [ ] Implement the fleet panels and preserve their existing no-value text and source metrics.
- [ ] Implement Kubernetes and VIP-service Sankey panels with descriptions naming `netsage-sankey-panel` and its catalogue URL.
- [ ] Re-run focused suites and the full Python dashboard suite; expect pass.
- [ ] Commit these domain changes and tests as `feat(dashboards): add fleet and security visual views`.

### Task 4: Health state, distribution, and cardinality cost

**Files:** Modify `deploy/grafana/gen/tabs/health_collection.py`, `deploy/grafana/gen/tabs/cardinality.py`; extend `deploy/grafana/gen/test_specialist_panels.py` and existing self-observability coverage tests.

**Interfaces:** Convert `Scrape success by collector` to core `state-timeline`. Add `Capability availability history` from `tailscale2otel_capability_status_ratio` grouped by collector and state. Add `Scrape duration distribution` from `sum by (le) (rate(tailscale2otel_scrape_duration_histogram_seconds_bucket<filters>[$__rate_interval]))`. Add plugin `Active series by metric family` from a top-N instant table over `tailscale2otel_active_series`, ordered metric_name then Value, using treemap label metric_name and size Value.

- [ ] Write failing tests for state-timeline groups and source metrics, heatmap retention of `le`, treemap group/version `marcusolsson-treemap-panel`/`2.1.1`, top-N bound, and catalogue description.
- [ ] Run the focused tests; expect missing-panel or wrong-kind failures.
- [ ] Implement state timelines, heatmap, and treemap while retaining current capability tables, scrape quantiles, cardinality tables, bars, and history.
- [ ] Run focused tests, the full Python dashboard suite, and `just promql`; expect pass.
- [ ] Commit the health and cost slice as `feat(dashboards): add state and cost visualisations`.

### Task 5: Generate, review, publish, and prove the result

**Files:** Regenerate both `deploy/grafana/*.json` artifacts and any alert-link/docs outputs changed transitively by `just gen-dashboards`; update TSO-0150 only through Backlog CLI.

- [ ] Run `just gen-dashboards`, inspect every generated diff, and run it a second time; expect the second run to leave no diff.
- [ ] Run targeted Python tests, `just promql`, `just --fmt --check`, then `just check`; record exact outputs and skipped checks.
- [ ] Run `just review-sharded base=main`. Read every finding; fix critical/major and every lower-severity issue that matters in context, then rerun affected checks.
- [ ] Review the final diff once for panel titles, alert links, conditional rendering, boundedness, plugin versions, and public-repository hygiene.
- [ ] Update TSO-0150 evidence, commit complete owned paths as `feat(dashboards): diversify dashboard visualisations`, and push `main`.
- [ ] Capture the exact pushed SHA and matching GitHub Actions run IDs; do not represent skipped or cancelled jobs as pass.
- [ ] Verify the exact SHA reached `m7kni/gc-gitsync-m7kni` by reading its main tree, not from workflow conclusion alone.
- [ ] Inspect both rendered dashboards on m7kni in a real browser. Verify Sankey, geomap, pie, state-timeline, heatmap, treemap, rollup/raw conditions, and PII conditions; report unavailable-data cases separately.
- [ ] Read `backlog instructions task-finalization`, check criteria only against evidence, write the final summary, mark Done only when proven, commit the tracker update by exact path, and push.

## Plan correction from self-review

In Task 4, `Active series by metric family` must query `topk($topn, max by (metric_name) (tailscale2otel_series_active))`, not the nonexistent `tailscale2otel_active_series`. Its ordered table fields are `metric_name`, then `Value`; the treemap labels by `metric_name` and sizes by `Value`.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Design approved in chat on 2026-09-19. Audit baseline: 502 generated panels; m7kni confirms Sankey 1.1.4 and Treemap 2.1.1 installed. Written design is doc-0006 and awaits review before implementation planning.

Written spec approved by Rob on 2026-09-19. Native same-session execution selected by the instruction to continue autonomously; implementation begins after plan review per the writing-plans gate.

Plan self-review completed: spec coverage, placeholder scan, interface names, gates, and Review Focus tests checked. Corrected the cardinality metric name through an append-only plan amendment.

Validation and delivery evidence (2026-09-19):

- Implementation landed in five commits, ending at code SHA `bc2fd2ab56706ea26c7c9746b2c56e6b502e5927`.
- Local gates passed: 234 dashboard tests, 134 alert tests, 48 script tests, 716 PromQL expressions, all formatter/lint/vet/race/tidy/vulnerability legs, and the complete `just check` gate. `just --fmt --check` passed. A final `just gen` completed with `git diff --exit-code` clean.
- Sharded CodeRabbit review completed 3/3 shards with zero findings.
- GitHub Actions CI run `35439589558` and Grafana sync run `35439589542` both completed successfully against the exact code SHA.
- GitSync mirror commit `c7a4e5465a979c11585b4b3183b65d3d330f3443` names the exact source SHA; the deployed Tailnet and health dashboard bytes matched the generated artifacts.
- Native Chrome verification on m7kni confirmed the rollup Sankey renders with live paths; flow composition pies, fleet geomap, device state timeline, compliance pie, VIP Sankey, scrape and capability state timelines, scrape heatmap, and cardinality treemap all render without plugin-load errors. The raw-flow and Kubernetes Sankey rows were absent because their live capability gates were false; generator contract tests prove both panels, their top-N bounds, and their existing signal/PII gates.
- Optional panels identify `netsage-sankey-panel` 1.1.4 and `marcusolsson-treemap-panel` 2.1.1, remain isolated in dedicated rows, and retain adjacent native evidence when a plugin is unavailable.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Diversified both generated Grafana dashboards with purpose-built Sankey, pie, geomap, state-timeline, heatmap, and treemap panels while preserving native evidence, top-N bounds, raw/rollup separation, signal-presence gates, and PII gates. Verified deterministic generation and the full local gate, completed a clean sharded CodeRabbit review, proved exact-SHA CI and GitSync delivery, and visually exercised both deployed dashboards in native Chrome on m7kni.
<!-- SECTION:FINAL_SUMMARY:END -->
