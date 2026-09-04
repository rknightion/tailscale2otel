---
title: Alerts
description: Deploy and tune the Grafana-managed alert and recording rules supplied with tailscale2otel for tailnet monitoring.
tags:
  - Alerting
---

# Alerts

Start with [Alert Profiles](alert-profiles.md). The committed Grafana-managed
set is the **recommended** profile: a high-signal default that leaves
environment-specific rules paused. `baseline` is the smaller first-on-call set;
`strict` enables nearly everything and needs a deliberate local render.

An **alert rule** evaluates a condition and opens an incident when it stays true
for its configured window. A **recording rule** continuously writes a derived
time series for dashboards or other alerts; it does not notify anyone by itself.
In the Grafana-managed format, **paused** means the rule is kept as configured
but is not evaluated. A **profile** is the named choice of which Grafana rules
start enabled; it does not alter their expressions or notification labels.

Choose the delivery artifact that matches the rule engine:

- [`deploy/alerts/grafana-managed/`](https://github.com/rknightion/tailscale2otel/tree/main/deploy/alerts/grafana-managed)
  is the recommended Grafana-managed set. It spans Prometheus and Loki and
  preserves each rule's `noDataState`, `execErrState`, and `paused` state.
- [`deploy/alerts/prometheus/tailscale2otel.rules.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/prometheus/tailscale2otel.rules.yaml)
  is the supported committed artifact for Prometheus, Mimir, and Cortex rulers.
  It contains the Prometheus-backed alert and recording rules with normalized
  metric names and `runbook_url` annotations. Prometheus cannot represent
  Grafana's per-rule pause state and cannot run the omitted Loki rules.

Push the Grafana-managed directory with [`gcx`](https://github.com/grafana/gcx):

```bash
gcx resources push -p deploy/alerts/grafana-managed
```

## Generated, not hand-written

The manifests are **generated** by
[`gen/build_rules.py`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/gen/build_rules.py).
Edit the generator, not the JSON, and regenerate with:

```bash
python3 deploy/alerts/gen/build_rules.py --out deploy/alerts/grafana-managed
```

Every `*.json` in the output directory is deleted before writing, so a renamed or removed rule
cannot linger as a stale file that keeps getting pushed. The Prometheus-compatible file is generated
alongside it with `just gen promrules`. Both outputs are deterministic and checked
for drift in CI.

Every rule follows the canonical Grafana 3-node pipeline (A query → B reduce → C threshold),
expressed as the `spec.expressions` map keyed by refId with `C` marked `"source": true`. Datasource
UIDs default to the portable Grafana Cloud defaults (`grafanacloud-prom` / `grafanacloud-logs`);
swap them for a self-hosted stack.

Rules are organised into five families:

- **`tailscale2otel-health`** — exporter self-health (scrape staleness, cardinality cap, API auth
  failures, checkpoint errors, enrichment cache age, and more)
- **`tailscale2otel-security`** — tailnet security and governance (tailnet-lock errors, key expiry,
  posture coverage, unverified contacts). The ACL risk-scoring gauges and the curated
  `tailscale.config.audit.changes` counter are natural additions here — e.g.
  `tailscale_acl_unrestricted_rules_ratio > 0` (any-to-any non-deny rules),
  `tailscale_acl_ssh_wildcard_ratio > 0` (wildcard SSH rules), or
  `increase(tailscale_config_audit_changes_total{tailscale_audit_change="auth_provider"}[1h]) > 0`.
- **`tailscale2otel-integrations`** — MDM/EDR posture sync, log-stream delivery health, and a
  paused accepted-event staleness rule for continuously active source/signal pairs
- **`tailscale2otel-network`** — DERP relay usage, region latency, flow data presence and
  flow-log truncation, per-target node-metrics scrape health, and the webhook-only node IP-forwarding
  fault
- **`tailscale2otel-recording`** — precomputed recording rules (DERP byte fraction, posture ratios,
  total active series)

!!! danger "Format traps — three of these fail silently"
    The manifest format is **not** the `apiVersion: 1` provisioning format, and the differences do
    not announce themselves. `gen/validate_manifests.py` enforces all of them offline.

    | trap | the rule |
    | --- | --- |
    | state casing | **BOTH** `noDataState` and `execErrState` spell the OK state **`"Ok"`**. The API accepts only `["Error", "Ok", "Alerting", "KeepLast"]`. Corrected 2026-07-27 — this table previously claimed an asymmetry (`"OK"` for `execErrState`), and 19 rules passed every local check and were then rejected at push time. |
    | durations | Go-style strings — `"30m0s"`, `"1h0m0s"`, `"0s"`. `"5m"` is rejected, and `relativeTimeRange` bounds are durations here, not integer seconds. |
    | panel links | the paired `__dashboardUid__` / `__panelId__` **annotations**, `__panelId__` a **string**. Top-level `dashboardUid`/`panelId` are provisioning-only and `additionalProperties: false` rejects them. |
    | recording rules | no `annotations`, `for`, `condition`, `noDataState` or `execErrState`. The spec is exactly `{title, trigger, metric, expressions, targetDatasourceUID, labels, paused}`. |

!!! tip "Limit-agnostic cardinality alerting"
    Prefer `count(tailscale2otel_series_overflowing_ratio == 1) > 0` for a cardinality-overflow
    alert — it needs no hardcoded threshold and stays correct when `cardinality.metric_limit` is
    changed. `tailscale2otel_scrape_budget_ratio` (last scrape duration ÷ interval; nearing `1` =
    risk of interval overrun) is another `tailscale2otel-health` signal worth enabling.

!!! tip "Default-disabled by design"
    Only a high-signal starter set ships with `spec.paused: false`. The rest are
    `spec.paused: true` — enable them in the Grafana UI once your tailnet has the relevant data.
    Pausing is orthogonal to the evaluation policy below: a paused rule still carries its declared
    no-data and error semantics, it just is not evaluating yet.

!!! tip "Evaluation policy — absence and error are per-rule, not global"
    Every rule declares one of four policies, which fix its `noDataState` and `execErrState`
    together. Previously all of them were fail-open on error, so a broken datasource or a malformed
    query read as *healthy* across the entire pack.

    | policy | noData | execErr | for |
    | --- | --- | --- | --- |
    | `coverage_critical` | `Alerting` | `Alerting` | the rule exists to notice something stopped — absence IS the alert |
    | `core` | `NoData` | `Error` | always emitted by a running exporter, so absence is abnormal |
    | `optional` | `Ok` | `Error` | absence means "not configured", but an error is still a fault |
    | `advisory` | `Ok` | `Ok` | neither absence nor a transient error is actionable here |

    `ExporterDown` is the only `coverage_critical` rule. Per-collector scrape rules are `core`
    rather than `coverage_critical` on purpose: total absence means the exporter is gone, which
    `ExporterDown` already pages on, and promoting them would fan one outage into a page per
    collector.

    **Nothing here can watch Grafana's own ruler.** If the ruler stops evaluating, no rule fires,
    including the `coverage_critical` one. See
    [Runbooks — who watches the watcher](runbooks.md) for the three operator-owned answers.

!!! tip "Every alert links to a runbook and a panel"
    All 111 alert rules carry a `runbook_url` pointing at a section of
    [Runbooks](runbooks.md), and 107 of them carry the paired `__dashboardUid__`/`__panelId__`
    annotations naming a canonical panel. Panel ids are resolved **by title** against the generated
    dashboard at build time, and generation hard-fails on a title that matches zero or more than one
    panel — so a renumbered or renamed panel breaks the build instead of silently producing a dead
    link.

!!! tip "Alert on accepted data, not only a running receiver"
    `AcceptedIngestDataStale` compares `time()` with
    `tailscale2otel_ingest_last_event_timestamp_seconds`. It catches a receiver or poller that
    remains healthy while delivering old events. The rule ships paused because audit and webhook
    streams can be legitimately quiet. Enable it only for `source`/`signal` pairs expected to be
    continuous, add label filters, and tune the one-hour threshold to that workload. The paused
    `tailscale2otel:ingest_event_freshness_seconds` recording rule exposes the same calculation for
    custom alerts.

## Deploying

```bash
# push the whole directory (the folder manifest sorts first, so it lands first)
gcx resources push -p deploy/alerts/grafana-managed
```

Anything that speaks the Grafana app-platform API works too: `kubectl` against the Grafana API
server, or the Terraform `grafana_apps_rules_alertrule_v0alpha1` /
`grafana_apps_rules_recordingrule_v0alpha1` resources, which take the same spec.

`gcx resources validate -p deploy/alerts/grafana-managed` is a **client-side** check only —
`alertrules.rules.alerting.grafana.app` does not support server-side dry-run, so it confirms the
manifests parse and that the kind is served, nothing more. The spec check is
`python3 deploy/alerts/gen/validate_manifests.py`, which runs offline and in CI.

## Metric naming in rule expressions

All expressions query the **normalized Prometheus names** produced by the OTLP-to-Prometheus
translation, not raw OTEL names. Dots become underscores, monotonic counters gain `_total`, units
gain their suffixes (`s` → `_seconds`, `By` → `_bytes`, `d` → `_days`), and a gauge with unit `"1"`
becomes `*_ratio`. See [Metrics](metrics.md) for the full translation table.

!!! note "Non-Grafana backends"
    If you send metrics to a non-Grafana OTEL backend with different normalization rules, you will
    need to adjust the metric names in the rule expressions accordingly.

## Wiring notifications

Every rule sets a `severity` label (`critical` / `warning` / `info`), plus `service` and `domain`.
Wire those into your Grafana notification policy to fan alerts to the right contact points.

## Prometheus-compatible deployment and local execution

Load the committed Prometheus-compatible file into your Prometheus, Mimir, or Cortex ruler using
that ruler's normal rule-file configuration. It is generated from the same catalogue as the
Grafana-managed set, includes recording rules and `runbook_url` annotations, and deliberately
omits Loki-backed rules. Because Prometheus has no per-rule `paused` field, every included rule
evaluates once loaded.

`promtool test rules` is the only local check that **runs** rules rather than only parsing them:

```bash
just gen promrules
promtool check rules deploy/alerts/prometheus/tailscale2otel.rules.yaml
promtool test rules deploy/alerts/tests/*.yaml
```

The committed file renders `coverage_critical`'s "absence is the alert" semantics as an explicit
`or absent(...)` arm, which Grafana expresses as `noDataState: Alerting`.

The offline contract tests cover the rest:

```bash
python3 -m unittest discover -s deploy/alerts/gen -t deploy/alerts/gen
```
