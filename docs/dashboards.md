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
directory: two v2-schema dashboards for Grafana 13+, generated from code, and nothing else — the
four legacy single-purpose dashboards were removed (see below).

## Two dashboards, two questions

The dashboards split on the question they answer, not on data source:

| Dashboard | uid | File | Question it answers |
|---|---|---|---|
| **Tailscale2OTel — Tailnet** | `tailscale2otel-tailnet` | `tailscale2otel-tailnet.json` | Is my tailnet healthy — devices, network, security, policy? |
| **Tailscale2OTel — Exporter health** | `tailscale2otel-health` | `tailscale2otel-health.json` | Is the exporter itself healthy — collection, ingestion, delivery, runtime, cost? |

Open **Tailnet** for anything about the fleet, its network traffic, or its security posture. Open
**Exporter health** when you suspect data is missing, stale, or wrong, and you need to know whether
that's the exporter's fault rather than the tailnet's. Each dashboard carries a cross-link to the
other in its controls menu (**Exporter health →** / **← Tailnet**), so you don't need to remember
both uids.

Both use the **Grafana v2 dashboard schema** (`dashboard.grafana.app/v2`, Grafana 13+) with tabbed,
often nested navigation and **conditional rendering** — a tab or row only appears when its
underlying data is actually present, so each dashboard adapts to whichever optional collectors and
features you have enabled.

Both are **generated from code** (`gen/build.py` + `gen/dashboards.py`, standard-library Python).
Edit the generator, not the JSON, and regenerate with:

```bash
python3 gen/build.py --out-dir .
```

### `tailscale2otel-tailnet` tabs

Fleet & Network and Security & Policy are nested domains — each has sub-tabs, and one of them
(Devices, Security & Audit, Policy & Config) is nested a level deeper still.

| Tab path | What it covers |
|---|---|
| **Overview** | At-a-glance health: device counts, key expiry, ACL age, flow-logging state, audit/event rates, a features/settings matrix, an MSP multi-tailnet summary, and a security scorecard. |
| **Fleet & Network > Devices > Inventory & Hygiene** | Inventory, authorization/sharing split, trends, fleet hygiene roll-ups (stale, untagged, ephemeral, outdated, version/tag distributions), and device health tables. |
| **Fleet & Network > Devices > Posture & Security** | Per-device security flags (SSH, key-expiry-disabled, shared connections), key-expiry distribution, and device posture overview. |
| **Fleet & Network > Devices > Connectivity & Routing** | Connectivity/NAT quality, exit nodes, subnet routes, and DERP latency/region preference, at both fleet and per-device granularity. |
| **Fleet & Network > Network & Flows** | Flow summary, integrity and ingestion hygiene, exit-node I/O, bounded-rollup and raw throughput/top-talker sections (each shown only when present), and the flow log stream. |
| **Fleet & Network > Node Metrics** | Scraper health, per-node `tailscaled_*` traffic/drops/DERP-path series, and routing/health messages (conditionally rendered). |
| **Security & Policy > Security & Audit > Audit Trail** | Audit change rates, device churn, configuration-audit tables, actor/target correlation, and a Loki log explorer. |
| **Security & Policy > Security & Audit > Risk & ACL** | ACL-hygiene risk indicators (wildcard/unrestricted/auto-approver/SSH/posture-gated) and tailnet-lock status. |
| **Security & Policy > Security & Audit > Posture & Compliance** | MDM/EDR integration sync and match rate, device posture snapshot and attribute expiry, and security-posture coverage (auto-update, encryption). |
| **Security & Policy > Security & Audit > Identity & Keys** | Key and access expiry risk, device-share invites, key inventory/age, credential scope/blast-radius, and user/invite hygiene. |
| **Security & Policy > Policy & Config > Access & ACL** | ACL size, rules-by-section, auto-approver and posture-gated inventory, and policy validation status. |
| **Security & Policy > Policy & Config > DNS & Settings** | DNS (MagicDNS, resolvers, split-DNS, search paths) and tailnet settings/features. |
| **Security & Policy > Policy & Config > Identity & Credentials** | Users (by role/status/type), API keys and credential scopes, key expiry detail, and OAuth application inventory. |
| **Security & Policy > Policy & Config > Integrations** | VIP services, webhook endpoint inventory, GeoIP enrichment, and posture-integration last-sync errors. |
| **Security & Policy > Kubernetes Audit** | Kubernetes API request volume, sensitive-resource reads, exec/attach/portforward and terminal sessions, mutating requests, RBAC probes, schema drift, and investigation-focused log views (conditionally rendered — Kubernetes operator audit only). |

### `tailscale2otel-health` tabs

| Tab | What it covers |
|---|---|
| **Overview** | Golden signals (exporter up, collectors OK, goroutines, build info), collecting/delivering summaries, application health (config valid/warnings, uptime, checkpoint), a degradation summary, and API throttling. |
| **Collection** | Node-metrics scraper health, per-collector scrape duration/success/errors/staleness, API request/retry/latency and rate-limiter wait, capability & scope preflight, the Prometheus pull endpoint, the enrichment and rDNS caches, and per-tailnet API errors. |
| **Ingestion** | Object-store ingestion status/throughput/faults, the ingress WAL, per-entity subrequest fan-out, stream and webhook ingestion, receiver health and loss detail, ingestion volume, accepted-data freshness, cross-source dedup, processor queues, log truncation, and audit pipeline state/latency/schema drift. |
| **Delivery** | Annotation delivery, OTLP export health (latency/outcome/failures/spans), PII filter status, SIEM log-shipping health, and trace/span diagnostics. |
| **Runtime** | Go runtime (goroutines, GOMAXPROCS, uptime, CPU), GC & memory, profiling upload health, and TLS certificate rotation. |
| **Cost & Cardinality** | Cardinality cap & overflow, active series by group, series budget vs cap, ingest-vs-export cost, node-metrics name budget, flow-cardinality drivers, and cross-source dedup set size/evictions. |

## Removed: the four classic-schema dashboards

`tailscale-fleet.json` (`ts2otel-fleet`), `tailscale-network.json` (`ts2otel-network`),
`tailscale-audit-events.json` (`ts2otel-audit-events`) and `tailscale-exporter-health.json`
(`ts2otel-exporter-health`) have been **removed**. They were hand-maintained `schemaVersion: 39`
JSON, duplicated content the v2 dashboards already cover, and were excluded from every generator and
drift gate — so they drifted silently and were the only dashboards nothing tested.

tailscale2otel targets **Grafana 13+ and the v2 dashboard schema only**. The v2 dynamic layout
(tabs, nested navigation, conditional rendering) cannot be expressed in the classic schema, so a
compatibility copy could not have shown the same thing anyway.

If you had one of those UIDs provisioned, point at the two dashboards above instead. The tab that
replaces each is:

| Legacy dashboard | Now lives at |
|---|---|
| fleet | `tailscale2otel-tailnet`'s **Fleet & Network > Devices** sub-tabs |
| network | `tailscale2otel-tailnet`'s **Fleet & Network > Network & Flows** |
| audit events | `tailscale2otel-tailnet`'s **Security & Policy > Security & Audit > Audit Trail** (plus **Risk & ACL** and **Posture & Compliance** for the security-scoped panels) |
| exporter health | `tailscale2otel-health`'s **Collection**, **Ingestion**, **Delivery**, **Runtime** and **Cost & Cardinality** tabs |

Both dashboards' tabs hide themselves when their backing signal is absent, so you see less than the
old dashboards showed only where the old ones were rendering empty panels.

## Importing

!!! warning "Grafana 13+ only"
    Both files are **v2 resources** (`apiVersion: dashboard.grafana.app/v2`), not classic
    dashboards. Grafana 12.4 accepts them with a `200` and then **renders nothing**, and
    Grafana 11.5 rejects them with the misleading error `Dashboard title cannot be empty`. Neither
    failure mode says "your Grafana is too old", so check the version first.

    This also means the **classic `POST /api/dashboards/db` endpoint does not apply** — it takes a
    v1 dashboard body. Use one of the paths below.

**`gcx` (recommended):**

```bash
gcx resources push -f tailscale2otel-tailnet.json
gcx resources push -f tailscale2otel-health.json
```

**Grafana UI:** Dashboards → New → Import → Upload JSON file, then map the datasources.

**File provisioning:** drop the JSON into a path referenced by a `dashboards` provider and restart
Grafana.

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
