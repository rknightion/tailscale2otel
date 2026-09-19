---
id: doc-0006
title: TSO-0150 dashboard visualisation redesign
type: specification
created_date: '2026-09-19 10:03'
updated_date: '2026-09-19 10:05'
tags:
  - grafana
  - dashboards
  - design
---
# TSO-0150 Dashboard Visualisation Redesign

## Intent

Make the generated dashboard family answer relationship, composition, geography, state-change, distribution, and cost questions with visualisations suited to those questions. Diversity is a means, not a target. Existing tables, time series, logs, traces, and flame graphs remain where they already communicate the data correctly.

The dashboard remains portable across Grafana 13+ installations. Optional plugins may improve a view but must never remove the native panels that carry the same operational evidence.

## Evidence and constraints

- The generated family currently contains 502 panels. Time series, stat, table, bar chart, bar gauge, and logs account for nearly all of them.
- The m7kni stack has `netsage-sankey-panel` 1.1.4 and `marcusolsson-treemap-panel` 2.1.1 installed, along with FlowX, Graphviz, ECharts, calendar heatmap, Polystat, and other catalogue plugins.
- The live Cloudflare flow dashboard uses composition views alongside time series and top-N bars. The upstream NetFlow dashboard also uses pie charts and geomaps.
- Grafana conditional rendering can gate rows on signal presence, but it cannot test whether a panel plugin is installed. A missing plugin therefore produces the normal local panel error.
- Dashboard delivery remains GitSync only. No dashboard API or `gcx` push is permitted.

## Selection rules

1. A specialist panel must answer a question that is materially harder to answer in the current view.
2. Native Grafana panels are preferred when they express the same model.
3. Optional plugin panels sit beside retained native evidence rather than replacing it.
4. Queries remain bounded with top-N or existing cardinality controls.
5. Existing data-presence and PII gates apply unchanged to every new view.

## Dashboard changes

### Network and flows

- Add `Traffic topology - ROLLUP` using `netsage-sankey-panel`. The table frame is source node, destination node, and byte rate. It is bounded by `$topn`, gated by `has_rollup_flow`, hidden by the node-identity PII sentinel, and visible by default.
- Add `Traffic topology - RAW` from the raw per-flow metric family. It uses the same field contract, is gated by `has_raw_flow`, hidden by the node-identity PII sentinel, and is collapsed because raw mode is the expensive full-detail path.
- Add `Current flow share by transport` and `Current flow share by traffic type` as native pie charts. Retain the existing time series that show change over time.
- Retain top-talker bars, topology tables, and the raw Loki stream as functional fallbacks and investigation detail.

### Fleet operations

- Change `Devices by country` from a bar chart to a native geomap using the ISO country field renamed to `lookup`.
- Add `Device online state by node` as a state timeline using the per-device online metric. Apply the existing device-identity redaction gate and retain the aggregate online trend.
- Change `Devices by OS` and `Compliance distribution` to native pie charts. Their category sets are small and bounded. Ordered, high-cardinality, and exact-value views remain bars or tables.

### Security and integrations

- Add `Kubernetes request paths` as a Sankey for identity to verb to resource. The query uses a bounded top-N increase over the selected range, remains an attempt view rather than an outcome view, and is hidden by the email identity sentinel.
- Add `VIP service topology` as a Sankey for service to backing host. It is gated by service data and by the existing per-device identity sentinel.
- Leave raw event streams, policy inventories, configuration snapshots, and evidence tables unchanged.

### Health, runtime, and cost

- Change `Scrape success by collector` from a time series to a state timeline so failure transitions and flapping are visible.
- Add `Capability availability history` as a state timeline beside the existing current-state capability table.
- Add `Scrape duration distribution` as a native heatmap using `tailscale2otel_scrape_duration_histogram_seconds_bucket`, aggregated by `le` across collectors. Retain the per-collector p50, p95, and p99 time series.
- Add `Active series by metric family` using `marcusolsson-treemap-panel` in Cost and Cardinality. Retain the existing table and time series for exact values and history.

### Tabs intentionally unchanged

Policy and DNS inventories, credential tables, raw audit logs, trace waterfall, flame graphs, delivery latency, API latency, ingestion latency, and detailed health tables already use the appropriate visual model or cannot preserve their important dimension in a single specialist panel. The implementation audit records these as reviewed with no change rather than introducing decorative panels.

## Generator design

- Extend the panel builder with explicit helpers or option blocks for pie chart, geomap, state timeline, heatmap, Sankey, and treemap configurations.
- Allow optional plugins to stamp their real plugin version instead of the core Grafana nominal version.
- Extend the organize transformation to support deterministic field ordering because Sankey consumes columns from left to right and the numeric value field last.
- Keep every new panel in the existing single-process family build so coverage, unique-title, alert-link, sentinel, and generated-drift checks continue to see the complete dashboard set.

## Missing-plugin behaviour

Core row titles name the exact plugin ID and remain visible when the plugin is absent. Panel descriptions include the Grafana catalogue URL. Grafana then shows its normal missing-plugin error only inside that panel. Adjacent native panels continue to work, so no telemetry or investigation path depends on plugin installation.

## Verification

1. Add focused generator tests for panel kind, options, field order, query bounds, conditional rendering, and PII gates.
2. Regenerate both Grafana v2 artifacts and run dashboard generator tests plus PromQL validation.
3. Run `just gen`, confirm no second-pass diff, and run `just check`.
4. Run the repository sharded CodeRabbit review because generator logic has branching.
5. Push through the normal main workflow, verify the exact GitSync source commit, and inspect the rendered panels on m7kni with representative rollup and raw data.

## Non-goals

- No automatic plugin installation.
- No dashboard API or `gcx` dashboard push.
- No custom ECharts JavaScript or HTML panels.
- No changes to telemetry emission, cardinality controls, or PII policy.
- No replacement of precise tables with charts that make exact values harder to read.
