---
title: Alerts
description: Prometheus / Grafana-managed alert rules shipped with tailscale2otel
tags:
  - Alerting
---

# Alerts

tailscale2otel ships ready-to-use alert and recording rules in
[`deploy/alerts/`](https://github.com/rknightion/tailscale2otel/tree/main/deploy/alerts).
Two delivery models are provided — pick **one** per rule set; loading both causes double-firing.

## Two delivery models

| File | Format | Evaluated by |
|---|---|---|
| [`tailscale2otel.grafana-rules.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/tailscale2otel.grafana-rules.yaml) | Grafana file-provisioning (`apiVersion: 1`) | Grafana (can span Prometheus + Loki) |
| [`tailscale2otel.rules.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/tailscale2otel.rules.yaml) | Standard Prometheus ruler `groups:` / `rules:` | Prometheus, Mimir, Cortex, or Loki ruler |

### Grafana-managed rules (recommended)

[`tailscale2otel.grafana-rules.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/tailscale2otel.grafana-rules.yaml)
is **generated** by [`gen/build_rules.py`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/gen/build_rules.py).
Edit the generator, not the YAML, and regenerate with:

```bash
python3 gen/build_rules.py --out tailscale2otel.grafana-rules.yaml
```

Every rule follows the canonical Grafana 3-node pipeline (A query → B reduce → C threshold) so it
round-trips cleanly through the Grafana UI and API. Datasource UIDs default to the portable Grafana
Cloud defaults (`grafanacloud-prom` / `grafanacloud-logs`); swap them for a self-hosted stack.

Rules are organised into five groups:

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
- **`tailscale2otel-network`** — DERP relay usage, region latency, flow data presence
- **`tailscale2otel-recording`** — precomputed recording rules (DERP byte fraction, posture ratios,
  total active series)

!!! tip "Limit-agnostic cardinality alerting"
    Prefer `count(tailscale2otel_series_overflowing_ratio == 1) > 0` for a cardinality-overflow
    alert — it needs no hardcoded threshold and stays correct when `cardinality.metric_limit` is
    changed. `tailscale2otel_scrape_budget_ratio` (last scrape duration ÷ interval; nearing `1` =
    risk of interval overrun) is another `tailscale2otel-health` signal worth enabling.

!!! tip "Default-disabled by design"
    Only a high-signal starter set ships with `isPaused: false`. The rest are `isPaused: true` —
    enable them in the Grafana UI once your tailnet has the relevant data. Pausing is orthogonal to
    the evaluation policy below: a paused rule still carries its declared no-data and error
    semantics, it just is not evaluating yet.

!!! tip "Evaluation policy — absence and error are per-rule, not global"
    Every rule declares one of four policies, which fix its `noDataState` and `execErrState`
    together. Previously all of them were `execErrState: OK`, so a broken datasource or a malformed
    query read as *healthy* across the entire pack.

    | policy | noData | execErr | for |
    | --- | --- | --- | --- |
    | `coverage_critical` | `Alerting` | `Alerting` | the rule exists to notice something stopped — absence IS the alert |
    | `core` | `NoData` | `Error` | always emitted by a running exporter, so absence is abnormal |
    | `optional` | `OK` | `Error` | absence means "not configured", but an error is still a fault |
    | `advisory` | `OK` | `OK` | neither absence nor a transient error is actionable here |

    `ExporterDown` is the only `coverage_critical` rule. Per-collector scrape rules are `core`
    rather than `coverage_critical` on purpose: total absence means the exporter is gone, which
    `ExporterDown` already pages on, and promoting them would fan one outage into a page per
    collector.

    **Nothing here can watch Grafana's own ruler.** If the ruler stops evaluating, no rule fires,
    including the `coverage_critical` one. See
    [Runbooks — who watches the watcher](runbooks.md) for the three operator-owned answers.

!!! tip "Every alert links to a runbook and a panel"
    All 73 alert rules carry a `runbook_url` pointing at a section of
    [Runbooks](runbooks.md), and 72 of them carry paired `dashboardUid`/`panelId` fields naming a
    canonical panel. Panel ids are resolved **by title** against the generated dashboard at build
    time, and generation hard-fails on a title that matches zero or more than one panel — so a
    renumbered or renamed panel breaks the build instead of silently producing a dead link.

!!! tip "Alert on accepted data, not only a running receiver"
    `AcceptedIngestDataStale` compares `time()` with
    `tailscale2otel_ingest_last_event_timestamp_seconds`. It catches a receiver or poller that
    remains healthy while delivering old events. The rule ships paused because audit and webhook
    streams can be legitimately quiet. Enable it only for `source`/`signal` pairs expected to be
    continuous, add label filters, and tune the one-hour threshold to that workload. The paused
    `tailscale2otel:ingest_event_freshness_seconds` recording rule exposes the same calculation for
    custom alerts.

#### Importing the Grafana-managed file

- **File provisioning** (self-hosted / Alloy): drop the file in
  `/etc/grafana/provisioning/alerting/` and restart Grafana.
- **Terraform / Grizzly**: the file uses the Grafana provisioning model, which both tools consume
  directly.
- **Grafana Cloud UI**: the file-provisioning format is not importable via the UI's "Import alert
  rules" flow — use the provisioning API or Terraform instead. For the UI path, use
  `tailscale2otel.rules.yaml` (see below).

### Datasource-managed baseline (`tailscale2otel.rules.yaml`)

[`tailscale2otel.rules.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/tailscale2otel.rules.yaml)
is the hand-maintained Prometheus-format equivalent: standard `alert:` / `expr:` / `for:` rules.
It covers the same core signals — exporter liveness, collector failures, OTLP export errors, device
and auth key expiry, flow-logging state, node-metrics target health, and a webhook-driven IP
forwarding misconfiguration alert.

**Prometheus / Mimir / Cortex ruler** — add it to your `rule_files:` or load with `mimirtool`:

```yaml
# prometheus.yml
rule_files:
  - /etc/prometheus/rules/tailscale2otel.rules.yaml
```

**Grafana UI** — Alerting → Alert rules → More → Import alert rules from a Prometheus rules file.

## Metric naming in rule expressions

All `expr` fields query the **normalized Prometheus names** produced by Grafana Cloud's OTLP
pipeline, not the raw OTEL names. The same rules apply as in the dashboards: dots become
underscores, counters gain `_total`, and a gauge with unit `"1"` becomes `*_ratio`. See
[Metrics](metrics.md) for the full translation table.

!!! note "Non-Grafana backends"
    If you send metrics to a non-Grafana OTEL backend with different normalization rules, you will
    need to adjust the metric names in the rule expressions accordingly.

## Wiring notifications

Both files set a `severity` label (`critical` / `warning` / `info`) on every rule. Wire that label
into your Grafana notification policy or Alertmanager routing tree to fan alerts to the right
contact points.

## Validating locally

```bash
# PromQL expression check (requires promtool)
promtool check rules deploy/alerts/tailscale2otel.rules.yaml
```
