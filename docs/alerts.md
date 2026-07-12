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

Rules are organised into four groups:

- **`tailscale2otel-health`** — exporter self-health (scrape staleness, cardinality cap, API auth
  failures, checkpoint errors, enrichment cache age, and more)
- **`tailscale2otel-security`** — tailnet security and governance (tailnet-lock errors, key expiry,
  posture coverage, unverified contacts). The ACL risk-scoring gauges and the curated
  `tailscale.config.audit.changes` counter are natural additions here — e.g.
  `tailscale_acl_unrestricted_rules_ratio > 0` (any-to-any non-deny rules),
  `tailscale_acl_ssh_wildcard_ratio > 0` (wildcard SSH rules), or
  `increase(tailscale_config_audit_changes_total{tailscale_audit_change="auth_provider"}[1h]) > 0`.
- **`tailscale2otel-integrations`** — MDM/EDR posture sync, log-stream delivery health
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
    enable them in the Grafana UI once your tailnet has the relevant data. Optional signals
    (posture, log streaming, tailnet-lock, DERP rollups) use `noDataState: OK` so they don't fire
    until data actually exists.

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
