---
title: Dashboards
description: Grafana dashboards shipped with tailscale2otel and how to import them
tags:
  - Grafana
  - Dashboards
---

# Dashboards

tailscale2otel ships ready-to-use Grafana dashboards in the
[`deploy/grafana/`](https://github.com/rknightion/tailscale2otel/tree/main/deploy/grafana)
directory. There are two tiers: a flagship v2 dashboard for Grafana 13+ and four legacy
single-purpose dashboards for older stacks.

## Flagship dashboard (`tailscale2otel.json`)

[`tailscale2otel.json`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/grafana/tailscale2otel.json)
is a comprehensive, single-file view of the entire tailnet. It uses the **Grafana v2 dashboard
schema** (`dashboard.grafana.app/v2`, Grafana 13+) with tabbed navigation and **conditional
rendering** — a tab or row only appears when its underlying data is actually present, so the
dashboard adapts to whichever optional collectors and features you have enabled.

The dashboard is **generated from code** (`gen/build.py`, standard-library Python). Edit the
generator, not the JSON, and regenerate with:

```bash
python3 gen/build.py --out tailscale2otel.json
```

### Tabs

| Tab | What it covers |
|---|---|
| **Overview** | At-a-glance health: device counts, key expiry, ACL age, flow-logging state, cardinality, and a features matrix. |
| **Fleet & Devices** | Inventory, OS breakdown, trends, device health tables, DERP latency, subnet routes, connectivity/NAT quality, exit-node and subnet-router analytics, and fleet hygiene roll-ups (untagged, ephemeral, tag, and version distributions). |
| **Network & Flows** | Flow summary, bounded rollup and raw throughput sections (each shown only when present), top talkers, and `__other__` share. |
| **Events & Logs** | Audit/webhook rates, stream health, a Loki log explorer, and dedicated flow/posture log streams. |
| **Security & Audit** | ACL-hygiene risk (wildcard/unrestricted/auto-approver/SSH/posture-gated), config-change and device-churn rates, device-share invites, MDM posture, posture-integration sync/match, key and access expiry, tailnet-lock, and audit metric-vs-log correlation. |
| **Policy & Config** | ACL (size + risk-scoring), DNS, settings, users (by role/status/type), and key inventory (auth keys, OAuth clients, and API tokens). |
| **Node Metrics** | Per-node scraper health and forwarded `tailscaled_*` series (conditionally rendered). |
| **Tailnets** | MSP scorecard (one row per tailnet: online devices, scrape staleness, API errors) — appears only on multi-tailnet deployments. |
| **Exporter Diagnostics** | Per-collector scrape duration/success/errors, API request stats, cardinality, enrichment cache, and Go runtime. |
| **Cardinality & Cost** | Per-metric series vs cap, overflow table, series-by-group cost driver, per-metric headroom, flow-cardinality drivers, dedup sets, and an ingest-vs-export DPM/LPM cost view. |

## Legacy dashboards (Grafana ≤12 friendly)

Four classic-schema dashboards (`schemaVersion: 39`) remain for older Grafana or simpler
deployments. They use `${DS_PROM}` / `${DS_LOKI}` template variables so they are datasource-agnostic.

| File | UID | Purpose |
|---|---|---|
| [`tailscale-fleet.json`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/grafana/tailscale-fleet.json) | `ts2otel-fleet` | Device fleet & inventory |
| [`tailscale-network.json`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/grafana/tailscale-network.json) | `ts2otel-network` | Network flow throughput & top talkers |
| [`tailscale-audit-events.json`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/grafana/tailscale-audit-events.json) | `ts2otel-audit-events` | Audit + webhook events and log streams |
| [`tailscale-exporter-health.json`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/grafana/tailscale-exporter-health.json) | `ts2otel-exporter-health` | Exporter self-observability |

## Importing

**Grafana UI:** Dashboards → New → Import → Upload JSON file, then map the datasources.

**HTTP API:**

```bash
curl -sS -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$(jq '{dashboard: ., overwrite: true}' tailscale-fleet.json)" \
  "$GRAFANA_URL/api/dashboards/db"
```

**File provisioning:** drop the JSON into a path referenced by a `dashboards` provider and restart Grafana.

**`gcx` (Grafana Cloud):**

```bash
gcx dashboards create -f tailscale2otel.json       # first time
gcx dashboards update tailscale2otel -f tailscale2otel.json  # subsequent
```

## Datasource variables

All dashboards resolve datasources by template variable — no pinned UIDs — so they are portable across stacks:

- `${DS_PROM}` / `ds_prometheus` — a Prometheus datasource (Grafana Cloud default UID: `grafanacloud-prom`)
- `${DS_LOKI}` / `ds_loki` — a Loki datasource (Grafana Cloud default UID: `grafanacloud-logs`)

## OTLP → Prometheus naming

Grafana Cloud's OTLP ingest pipeline normalizes metric names before they reach PromQL. The dashboard
queries use the **normalized** names, not the raw OTEL names. The key rules:

- Dots become underscores in both metric names and label keys.
- Monotonic counters gain a `_total` suffix.
- Units are appended: `By` → `_bytes`, `s` → `_seconds`, `d` → `_days`.
- A gauge with unit `"1"` gets a `_ratio` suffix — including plain integer counts, so
  `tailscale_devices_count` becomes `tailscale_devices_count_ratio`.

See [Metrics](metrics.md) for the full naming rules and the complete metric catalog.

!!! note "Slowly-scraped gauges"
    Config gauges (ACL, DNS, settings, keys, users) are read through
    `last_over_time(<metric>[<window>])` so panels show the latest known value even when the
    most recent sample is older than Prometheus' 5-minute staleness window.
