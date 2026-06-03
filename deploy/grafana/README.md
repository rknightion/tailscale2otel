# tailscale2otel — Grafana Dashboards

Standalone, importable Grafana **13** dashboards for the telemetry emitted by
`tailscale2otel`. Each file is a self-contained dashboard JSON
(`schemaVersion: 39`) tagged `tailscale` / `tailscale2otel`.

## Dashboards

| File | UID | Purpose |
| --- | --- | --- |
| `tailscale-fleet.json` | `ts2otel-fleet` | Device fleet & inventory: online/offline counts, devices-over-time, updates available, OS breakdown, DERP latency, device & auth-key expiry, last-seen, subnet routes. |
| `tailscale-network.json` | `ts2otel-network` | Network flow throughput: bytes/packets/flows rates by direction / transport / traffic type, top-N source & destination talkers, and a row repeated per traffic type. |
| `tailscale-audit-events.json` | `ts2otel-audit-events` | Audit + webhook event stream: Loki log panels (audit / flow / switchable event), audit & webhook counter rates, stream ingest vs rejected, log volume by event name. |
| `tailscale-exporter-health.json` | `ts2otel-exporter-health` | Exporter self-observability: up state, scrape duration / success / errors by collector, API request status codes, retries, export failures, enrich cache age/size, build info. |

## Importing

Grafana UI: **Dashboards -> New -> Import -> Upload JSON file**, pick the file,
then map the datasource variables (see below) and click *Import*.

Via API / provisioning:

```bash
# wrap in {"dashboard": <json>, "overwrite": true} for the HTTP API
curl -sS -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$(jq '{dashboard: ., overwrite: true}' tailscale-fleet.json)" \
  "$GRAFANA_URL/api/dashboards/db"
```

For file-based provisioning, drop the JSON files into a path referenced by a
`dashboards` provider in your Grafana provisioning config.

## Datasource variables (portability)

Every panel references datasources **by template variable**, so dashboards are
not pinned to a specific datasource UID:

- `${DS_PROM}` — a `prometheus` datasource (default in Grafana Cloud:
  `grafanacloud-prom`). Used by every dashboard.
- `${DS_LOKI}` — a `loki` datasource (default in Grafana Cloud:
  `grafanacloud-logs`). Used only by `tailscale-audit-events.json` for the log
  panels.

On import, Grafana prompts you to choose the concrete datasources for these
variables. Logs are matched on the label `service_name="tailscale2otel"`, and
the OTEL event type is the `event_name` label (normalized from the `event.name`
attribute).

## Template variables & dynamic layout

The dashboards lean on Grafana 13 multi-value variables, regex/All matching, and
repeated rows:

- **fleet** — `os_type`, `host_name`, `tailscale_user` (multi + All, chained).
- **network** — `network_transport`, `tailscale_traffic_type`, `src_node`,
  `topn` (top-N selector), and `repeat_traffic_type` which **repeats a row per
  traffic type**.
- **audit-events** — `event_name` (switches the Loki log stream),
  `audit_action`, `webhook_type`, and a `log_search` textbox regex filter.
- **exporter-health** — `tailscale_collector`, `endpoint` (multi + All).

All `rate()` queries use `$__rate_interval`; Loki rate panels use `$__auto`.

## OTLP → Prometheus naming (important)

Metric names come from OpenTelemetry and are normalized by Grafana Cloud's
OTLP → Prometheus pipeline, so the **queries use the normalized names**, not the
raw OTEL names:

- Dots become underscores in both metric names **and** attribute (label) keys.
- Monotonic counters get a `_total` suffix.
- Units are appended: `By` → `_bytes`, `s` → `_seconds`, `d` → `_days`.
- A gauge with unit `"1"` gets a `_ratio` suffix.

**Known quirk:** because unit `"1"` always yields `_ratio`, plain *counts* that
were declared with unit `"1"` also end up named `*_ratio` even though they are
raw counts, not fractions. Examples in these dashboards:

- `tailscale_devices_count_ratio`, `tailscale_users_count_ratio`,
  `tailscale_keys_count_ratio` — integer counts, not ratios.
- `tailscale2otel_enrich_cache_size_ratio` — number of cache entries.
- `tailscale_device_online_ratio`, `tailscale2otel_up_ratio`,
  `tailscale_device_update_available_ratio` — boolean 0/1 gauges.
- `tailscale2otel_build_info_ratio` — always `1`; the metadata is in the
  `service_version` / `go_version` labels (info-style metric).

`*_seconds` expiry/last-seen gauges hold **absolute epoch timestamps**. The
panels convert them to relative time at query level, e.g.
`tailscale_device_key_expiry_seconds - time()` (seconds until expiry) or
`time() - tailscale_device_last_seen_seconds` (age since last seen).

## Notes & caveats

- Route panels (`tailscale_device_routes_advertised_routes` /
  `_enabled_routes`) and posture logs are **gated** behind the exporter's
  `collect_routes` / `collect_posture` options; those panels are empty if the
  features are disabled.
- The verbatim node-metrics scraper forwards original `tailscaled` series under
  their original names plus an `instance` label and is **not** covered here (no
  fixed schema); add panels ad hoc as needed.
