# tailscale2otel — Grafana Dashboards

Grafana dashboards for the telemetry emitted by `tailscale2otel`.

## Flagship: `tailscale2otel.json` (Grafana 13+)

`tailscale2otel.json` is the **comprehensive, single-dashboard** view of the entire
project. It uses the **Grafana dashboard schema v2** (`dashboard.grafana.app/v2`,
Grafana 13+) with **tabs** and **dynamic (conditional) rendering**, so a section
only appears when its data is actually present in the target stack.

It is **generated** from `gen/build.py` (dashboards-as-code) — edit the generator,
not the JSON. Only the Python standard library is required.

### Tabs

| Tab | Covers |
| --- | --- |
| **Overview** | At-a-glance tailnet + exporter health: device counts, key expiry, ACL age, flow-logging state, scrape/export errors, active-series (cardinality), throughput, audit/flow activity, and a features/settings matrix. |
| **Fleet & Devices** | Inventory (online/offline/updates/users, OS breakdown), trends, device health tables (updates, key expiry, last-seen), DERP latency, subnet routes¹, device posture¹. |
| **Network & Flows** | Flow summary, then **ROLLUP** (bounded top-N) and **RAW** (full-cardinality) throughput/talkers sections — each shown only when that flow-metric mode is present — plus unique-peer/port counts and the `__other__` rollup share. |
| **Events & Logs** | Audit/webhook event rates, stream-ingestion health¹, a Loki log explorer (switchable by `event_name` + free-text filter), and dedicated flow- and posture-log streams. |
| **Policy & Config** | ACL (last-changed/size/rules), DNS (MagicDNS/nameservers/search/zones), settings & features, users (by role/status/type + per-user¹), and API keys (by type + expiry). |
| **Node Metrics**¹ | Per-node scraper health and the forwarded `tailscaled_*` series (in/out bytes & packets, routes, health messages, DERP region, peer-relay endpoints). |
| **Exporter Diagnostics** | Liveness/build, per-collector scrape duration/success/errors, API requests by status/endpoint, cardinality & dedup, enrichment cache, and the Go runtime (heap/GC/goroutines). |

¹ Conditionally rendered — appears only when that feature's data is present.

### How dynamic rendering works

Hidden presence variables (`has_*`) run `label_values(<metric>, __name__)` and
resolve to the metric name when ≥1 series exists, else empty. Rows/tabs carry a
v2 `ConditionalRendering` rule (`ConditionalRenderingVariable`, `matches .+`) on
that variable, so they show only when the data exists. This is evaluated both
live **and** by the image renderer (unlike `ConditionalRenderingData`, which the
static renderer does not evaluate).

Slowly-scraped config gauges (ACL/DNS/settings/keys/users) are read through
`last_over_time(<metric>[<window>])` so panels show the latest known value even
when the most recent sample is older than Prometheus' 5-minute staleness window.

### Regenerate & deploy

```sh
# regenerate the portable, folder-agnostic artifact (committed here)
python3 gen/build.py --out tailscale2otel.json

# push to a Grafana stack with gcx (optionally pin a folder UID)
gcx dashboards create -f tailscale2otel.json                 # first time
gcx dashboards update tailscale2otel -f tailscale2otel.json  # subsequent

# render a snapshot for visual review (Grafana image renderer)
gcx dashboards snapshot tailscale2otel --since 6h --width 1920 --theme dark

# focused, full-page preview of a single tab (the image renderer only captures
# the first tab of a tabbed dashboard, and struggles to capture many dense
# panels at once — render one tab at a time for review):
python3 gen/build.py --tab "Network & Flows" --uid t2 --out /tmp/t.json
gcx dashboards create -f /tmp/t.json && gcx dashboards snapshot t2-prev-network-flows --since 6h
```

> Snapshot note: static PNG rendering of dense, variable-heavy tabs (Fleet,
> Policy, Node Metrics) can show "No data" on lower panels because the renderer
> screenshots before all queries resolve. This is a renderer limitation, not a
> dashboard defect — the panels populate normally in the live UI. Render a single
> tab (`--tab`) for clean previews.

## Legacy standalone dashboards (Grafana ≤12 friendly)

The original four single-purpose dashboards remain for older Grafana or simpler
use. They are classic schema (`schemaVersion: 39`), importable, and datasource-
agnostic via `${DS_PROM}` / `${DS_LOKI}`.

| File | UID | Purpose |
| --- | --- | --- |
| `tailscale-fleet.json` | `ts2otel-fleet` | Device fleet & inventory. |
| `tailscale-network.json` | `ts2otel-network` | Network flow throughput & top talkers. |
| `tailscale-audit-events.json` | `ts2otel-audit-events` | Audit + webhook events and log streams. |
| `tailscale-exporter-health.json` | `ts2otel-exporter-health` | Exporter self-observability. |

## Importing (UI / API / provisioning)

UI: **Dashboards → New → Import → Upload JSON file**, then map the datasources.

```bash
# HTTP API (wrap in {"dashboard": <json>, "overwrite": true})
curl -sS -H "Authorization: Bearer $GRAFANA_TOKEN" -H "Content-Type: application/json" \
  -d "$(jq '{dashboard: ., overwrite: true}' tailscale-fleet.json)" \
  "$GRAFANA_URL/api/dashboards/db"
```

For file-based provisioning, drop the JSON into a path referenced by a
`dashboards` provider. Logs are matched on `service_name="tailscale2otel"`, and
the OTEL event type is the `event_name` label.

## Datasource variables (portability)

Panels reference datasources by template variable, not a pinned UID:

- `${DS_PROM}` / `ds_prometheus` — a `prometheus` datasource (Grafana Cloud default UID `grafanacloud-prom`).
- `${DS_LOKI}` / `ds_loki` — a `loki` datasource (Grafana Cloud default UID `grafanacloud-logs`).

## OTLP → Prometheus naming (important)

Metric names are normalized by Grafana Cloud's OTLP → Prometheus pipeline, so the
**queries use the normalized names**, not the raw OTEL names:

- Dots become underscores in both metric names **and** attribute (label) keys.
- Monotonic counters get a `_total` suffix.
- Units are appended: `By` → `_bytes`, `s` → `_seconds`, `d` → `_days`.
- A gauge with unit `"1"` gets a `_ratio` suffix.

**Known quirk:** because unit `"1"` always yields `_ratio`, plain *counts*
declared with unit `"1"` also end up named `*_ratio` even though they are raw
counts, not fractions — e.g. `tailscale_devices_count_ratio`,
`tailscale_users_count_ratio`, `tailscale2otel_enrich_cache_size_ratio`, and the
boolean 0/1 gauges `tailscale_device_online_ratio`, `tailscale2otel_up_ratio`.
`tailscale2otel_build_info_ratio` is always `1` (metadata in labels).

`*_seconds` expiry/last-seen gauges hold **absolute epoch timestamps**; panels
convert them at query level, e.g. `tailscale_device_key_expiry_seconds - time()`
(seconds until expiry) or `time() - tailscale_device_last_seen_seconds` (age).

## Notes & caveats

- Route, posture, node-metrics, stream and webhook sections are **gated** behind
  the matching exporter options; on the flagship dashboard they are hidden when
  absent, and on the legacy dashboards their panels are simply empty.
- Flow metrics can be emitted as bounded **rollup**, **raw**, or **both** — the
  flagship Network tab shows whichever is present.
