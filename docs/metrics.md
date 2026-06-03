# Metrics & Logs Reference

This is the as-built reference for every telemetry signal `tailscale2otel` emits: metrics
(exported as OTLP and, on Grafana Cloud, normalized into Prometheus series) and structured log
records (exported as OTLP logs, landing in Loki). It documents the OTEL source names, their units
and instrument types, the **normalized** Prometheus names you actually query in Grafana Cloud, the
key attributes/labels on each signal, and the conditions under which optional signals appear.

If you are wiring dashboards or alerts, query against the **Prometheus (normalized) name** column —
that is what exists in the metrics store. The OTEL name is the source-of-truth identifier used in
the code and in any non-Grafana OTEL backend.

---

## Naming conventions

### OpenTelemetry semantic-convention naming (the source names)

All metrics and log attributes are authored to follow
[OpenTelemetry semantic conventions](https://opentelemetry.io/docs/specs/semconv/):

- **Dotted, lowercase, namespaced names** — e.g. `tailscale.network.io`,
  `tailscale.device.online`, `tailscale2otel.scrape.duration`. Words within a segment use
  `snake_case` where needed (e.g. `last_seen`, `key.expiry`).
- **UCUM units** — units are expressed in the
  [Unified Code for Units of Measure](https://ucum.org/): `By` (bytes), `s` (seconds), `d` (days),
  `1` (a dimensionless ratio/flag), and "annotation" units like `{packet}`, `{flow}`, `{route}`,
  `{event}`, `{record}` for dimensionless counts of a thing.
- **No `_total` suffix in the source.** Monotonic counters are named without the Prometheus
  `_total` convention; that suffix is added later by the backend, not by us.
- **Attribute keys are dotted/namespaced too** — e.g. `network.io.direction`,
  `http.response.status_code`, `service.version`, `host.name`. Tailscale-specific keys use a
  `tailscale.*` prefix (e.g. `tailscale.src_node`, `tailscale.audit.action`).

### Grafana Cloud OTLP → Prometheus normalization

When OTLP metrics are ingested by Grafana Cloud (Mimir/Prometheus), the names and labels are
rewritten by the OTLP-to-Prometheus translation rules. The rules that matter here:

1. **Dots become underscores** — in both **metric names** *and* **attribute (label) keys**.
   `tailscale.network.io` → `tailscale_network_io`; the label `network.io.direction` →
   `network_io_direction`; `http.response.status_code` → `http_response_status_code`.
2. **Monotonic counters get a `_total` suffix.** `tailscale.network.io` (counter) →
   `tailscale_network_io..._total`.
3. **Units are appended to the name** for known UCUM units:
   - `By` → `_bytes`
   - `s` → `_seconds`
   - `d` → `_days`
4. **A unit of `1` on a gauge gets a `_ratio` suffix.** This is meant for true ratios (0..1), but
   the translation applies it to **any** gauge whose unit is `1`.

> **Quirk — count gauges become `*_ratio`.** Several of our gauges are dimensionless *counts*
> (e.g. `tailscale.devices.count`, `tailscale.acl.rules`, `tailscale.dns.nameservers.count`) that
> carry unit `1` because UCUM has no "count" unit for a gauge. The normalizer therefore appends
> `_ratio` to them, so you end up with `tailscale_devices_count_ratio`,
> `tailscale_acl_rules_ratio`, etc. These are **counts, not ratios** — read the Description column.
> The same applies to boolean/flag gauges (online, enabled, available) which are `0`/`1` and also
> land as `*_ratio`. This is a known cosmetic artifact of the OTLP→Prometheus mapping; the values
> are correct, only the suffix is misleading.
>
> Note that annotation units in curly braces — `{packet}`/`{flow}`/`{event}`/`{route}` — are
> **dropped** entirely; they are never appended to the name, for **either** counters **or** gauges.
> So `tailscale.network.packets` (counter) → `tailscale_network_packets_total`, and
> `tailscale.device.routes.advertised` (gauge) → `tailscale_device_routes_advertised` (no `_routes`).

### Worked examples

| OTEL source | Instrument | Unit | Normalization steps | Prometheus name |
|---|---|---|---|---|
| `tailscale.network.io` | counter | `By` | dots→`_`, unit `By`→`_bytes`, counter→`_total` | `tailscale_network_io_bytes_total` |
| `tailscale.device.online` | gauge | `1` | dots→`_`, gauge unit `1`→`_ratio` | `tailscale_device_online_ratio` |
| `tailscale.device.last_seen` | gauge | `s` | dots→`_`, unit `s`→`_seconds` | `tailscale_device_last_seen_seconds` |
| `tailscale.devices.count` | gauge | `1` | dots→`_`, gauge unit `1`→`_ratio` (a *count*, despite the suffix) | `tailscale_devices_count_ratio` |
| `tailscale.setting.devices_key_duration` | gauge | `d` | dots→`_`, unit `d`→`_days` | `tailscale_setting_devices_key_duration_days` |

Labels follow the same dots→underscores rule, so the OTEL attributes `tailscale.src.node` /
`tailscale.dst.node` are queried as the labels `tailscale_src_node` / `tailscale_dst_node`.

---

## Metrics

Instrument column: **counter** = monotonic cumulative (rendered as `_total` in Prometheus, use
`rate()`/`increase()`); **gauge** = point-in-time value.

### Self-observability (`tailscale2otel.*`)

Emitted by the service about itself. Use these for health, scrape success, API behavior, and
exporter health.

| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale2otel.up` | `1` | gauge | `tailscale2otel_up_ratio` | — | Liveness flag: `1` while the service is running and reporting. |
| `tailscale2otel.build_info` | `1` | gauge | `tailscale2otel_build_info_ratio` | `service_version`, `go_version` | Constant `1` build-info gauge; version/runtime carried as labels. |
| `tailscale2otel.scrape.duration` | `s` | gauge | `tailscale2otel_scrape_duration_seconds` | `tailscale_collector` | Wall-clock duration of the last scrape, per collector. |
| `tailscale2otel.scrape.success` | `1` | gauge | `tailscale2otel_scrape_success_ratio` | `tailscale_collector` | `1` if the last scrape for that collector succeeded, else `0`. |
| `tailscale2otel.scrape.errors` | `1` | counter | `tailscale2otel_scrape_errors_total` | `tailscale_collector`, `error_type` | Count of scrape errors, by collector and error class. |
| `tailscale2otel.scrape.last_timestamp` | `s` | gauge | `tailscale2otel_scrape_last_timestamp_seconds` | `tailscale_collector` | Unix timestamp the last scrape *finished* (success **or** failure); pair with `scrape.success` to detect last-success staleness. |
| `tailscale2otel.api.requests` | `1` | counter | `tailscale2otel_api_requests_total` | `endpoint`, `http_response_status_code` | Tailscale API requests, by endpoint and HTTP status code. |
| `tailscale2otel.api.retries` | `1` | counter | `tailscale2otel_api_retries_total` | `endpoint` | API retry attempts, by endpoint. |
| `tailscale2otel.export.failures` | `1` | counter | `tailscale2otel_export_failures_total` | `error_type` | OTLP export failures, by error class. |
| `tailscale2otel.enrich.cache_age` | `s` | gauge | `tailscale2otel_enrich_cache_age_seconds` | — | Age of the device-enrichment cache (since last refresh). |
| `tailscale2otel.enrich.cache_size` | `1` | gauge | `tailscale2otel_enrich_cache_size_ratio` | — | Number of devices in the enrichment cache (a **count**, despite `_ratio`). |

### Network / flow (`tailscale.network.*`, `tailscale.config.audit.*`)

Aggregated, low-cardinality counters derived from flow logs and audit logs. The full-fidelity
per-connection detail is emitted as **log records** (see [Log events](#log-events)).

| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.network.io` | `By` | counter | `tailscale_network_io_bytes_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `source_port`/`destination_port` (if enabled) | Bytes transferred, by direction/transport/traffic type and src/dst node. |
| `tailscale.network.packets` | `{packet}` | counter | `tailscale_network_packets_total` | same dims as `network.io` | Packets transferred, same dimensions. |
| `tailscale.network.flows` | `{flow}` | counter | `tailscale_network_flows_total` | `network_transport`, `tailscale_traffic_type` | Count of distinct flows (lower-cardinality than io/packets). |
| `tailscale.config.audit.events` | `{event}` | counter | `tailscale_config_audit_events_total` | `tailscale_audit_action`, `tailscale_audit_origin` | Configuration-audit events, by action and origin. |

> Label gating on `network.io`/`network.packets`: `tailscale_src_node`/`tailscale_dst_node` are
> gated by `cardinality.flow_node_dims` (**on** by default); `source_port`/`destination_port` are
> gated by `cardinality.flow_include_ports` (**off** by default, as ports add cardinality).

### Devices (`tailscale.device.*`, `tailscale.devices.count`)

Per-device gauges plus a fleet roll-up. "id dims" below is shorthand for the common device-identity
attribute set: `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`.

| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.device.online` | `1` | gauge | `tailscale_device_online_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user` | `1` if the device is currently online, else `0`. |
| `tailscale.device.last_seen` | `s` | gauge | `tailscale_device_last_seen_seconds` | id dims | Unix timestamp the device was last seen. |
| `tailscale.device.key.expiry` | `s` | gauge | `tailscale_device_key_expiry_seconds` | id dims | Unix timestamp the device node key expires. |
| `tailscale.device.update_available` | `1` | gauge | `tailscale_device_update_available_ratio` | id dims | `1` if a Tailscale client update is available for the device. |
| `tailscale.device.derp.latency` | `s` | gauge | `tailscale_device_derp_latency_seconds` | `host_name`, `host_id`, `tailscale_derp_region`, `tailscale_derp_preferred` | Latency from the device to a DERP region; one series per region. |
| `tailscale.device.routes.advertised` | `{route}` | gauge | `tailscale_device_routes_advertised` | `host_name`, `host_id` | Number of subnet routes the device advertises. **Gated** by `collect_routes`. |
| `tailscale.device.routes.enabled` | `{route}` | gauge | `tailscale_device_routes_enabled` | `host_name`, `host_id` | Number of advertised routes that are enabled/approved. **Gated** by `collect_routes`. |
| `tailscale.devices.count` | `1` | gauge | `tailscale_devices_count_ratio` | `os_type`, `tailscale_authorized`, `tailscale_external` | Fleet device count (a **count**, despite `_ratio`), bucketed by OS/authorized/external. |

### Users (`tailscale.users.count`, `tailscale.user.*`, `tailscale.user_invites.count`)

User roll-ups and per-user gauges. Per-user "id dims" = `enduser_id`, `tailscale_user_login`.

| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.users.count` | `1` | gauge | `tailscale_users_count_ratio` | `tailscale_user_role`, `tailscale_user_status`, `tailscale_user_type` | User count (a **count**), bucketed by role/status/type. |
| `tailscale.user.devices` | `1` | gauge | `tailscale_user_devices_ratio` | `enduser_id`, `tailscale_user_login` | Number of devices owned by the user (a **count**). |
| `tailscale.user.connected` | `1` | gauge | `tailscale_user_connected_ratio` | `enduser_id`, `tailscale_user_login` | `1` if the user is currently connected, else `0`. |
| `tailscale.user.last_seen` | `s` | gauge | `tailscale_user_last_seen_seconds` | `enduser_id`, `tailscale_user_login` | Unix timestamp the user was last seen. |
| `tailscale.user_invites.count` | `1` | gauge | `tailscale_user_invites_count_ratio` | `tailscale_user_invite_role`, `tailscale_user_invite_accepted` | Outstanding/processed user invites (a **count**), by role and accepted flag. |

### Keys (`tailscale.key.*`, `tailscale.keys.count`)

| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.key.expiry` | `s` | gauge | `tailscale_key_expiry_seconds` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description` | Unix timestamp an auth/API key expires; one series per key. |
| `tailscale.keys.count` | `1` | gauge | `tailscale_keys_count_ratio` | `tailscale_key_type`, `tailscale_key_revoked`, `tailscale_key_invalid` | Key count (a **count**), bucketed by type/revoked/invalid. |

### Settings / ACL / DNS (`tailscale.setting.*`, `tailscale.acl.*`, `tailscale.dns.*`)

| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.setting.enabled` | `1` | gauge | `tailscale_setting_enabled_ratio` | `tailscale_setting_name` | `1` if the named tailnet setting is enabled, else `0`. |
| `tailscale.setting.devices_key_duration` | `d` | gauge | `tailscale_setting_devices_key_duration_days` | — | Configured device key expiry duration, in days. |
| `tailscale.acl.last_changed` | `s` | gauge | `tailscale_acl_last_changed_seconds` | — | Unix timestamp the ACL policy last changed (detected by ETag). |
| `tailscale.acl.size` | `By` | gauge | `tailscale_acl_size_bytes` | — | Size of the current ACL policy document, in bytes. |
| `tailscale.acl.rules` | `1` | gauge | `tailscale_acl_rules_ratio` | `tailscale_acl_section` | Number of rules per ACL section (a **count**, despite `_ratio`). |
| `tailscale.dns.nameservers.count` | `1` | gauge | `tailscale_dns_nameservers_count_ratio` | — | Number of configured nameservers (a **count**). |
| `tailscale.dns.search_paths.count` | `1` | gauge | `tailscale_dns_search_paths_count_ratio` | — | Number of DNS search paths (a **count**). |
| `tailscale.dns.split_zones.count` | `1` | gauge | `tailscale_dns_split_zones_count_ratio` | — | Number of split-DNS zones configured (a **count**). |
| `tailscale.dns.magic_dns` | `1` | gauge | `tailscale_dns_magic_dns_ratio` | — | `1` if MagicDNS is enabled, else `0`. |

### Features (`tailscale.feature.*`)

| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.feature.enabled` | `1` | gauge | `tailscale_feature_enabled_ratio` | `tailscale_feature` | `1` if the named tailnet feature is enabled, else `0`; one series per feature. |

### Receivers — stream & webhook (`tailscale.stream.*`, `tailscale.webhook.*`)

Health/throughput counters for the optional HEC log-stream receiver and the webhook receiver.

| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.stream.records` | `{record}` | counter | `tailscale_stream_records_total` | `type` (`flow`\|`audit`) | Records accepted by the HEC stream receiver, by stream type. |
| `tailscale.stream.rejected` | `{rejection}` | counter | `tailscale_stream_rejected_total` | `reason` (`auth`\|`unparsable`) | Records rejected by the stream receiver, by reason. |
| `tailscale.webhook.events` | `{event}` | counter | `tailscale_webhook_events_total` | `tailscale_webhook_type` | Webhook events accepted, by Tailscale event type. |
| `tailscale.webhook.rejected` | `1` | counter | `tailscale_webhook_rejected_total` | `reason` | Webhook deliveries rejected (e.g. bad HMAC), by reason. |

### Node metrics scraper (`tailscale.node.*` + forwarded series)

See the dedicated [Node metrics scraper](#node-metrics-scraper) section — these series are
forwarded verbatim from `tailscaled` and are not part of the curated catalog above.

---

## Log events

Structured OTEL log records. They are exported via OTLP and land in **Loki** under datasource uid
`grafanacloud-logs`, all tagged with the label `service_name="tailscale2otel"`.

The OTEL event type is carried in the log attribute **`event.name`**. After Loki normalization the
dot becomes an underscore, so you filter on **`event_name`** in LogQL (e.g.
`| event_name="tailscale.config.audit"`). The `event.name` *value* keeps its dots.

| `event.name` | When emitted | Severity rule | Key attributes |
|---|---|---|---|
| `tailscale.network.flow` | Per flow connection (in `per_connection` mode) or per record (in `per_record` mode). | INFO. | `source_address`, `source_port`, `destination_address`, `destination_port`, `network_transport`, `network_type`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `tailscale_node_id`, **`tailscale_node_hostname`** (when the node is resolved via the enrichment cache), tx/rx bytes + packets. |
| `tailscale.config.audit` | Per configuration-audit event. | **WARN** when the event carries an error; otherwise INFO. | `tailscale_audit_action`, `tailscale_audit_origin`, `tailscale_audit_event_group_id`, `enduser_id`, `tailscale_actor_login`, `tailscale_actor_display`, `tailscale_target_id`, `tailscale_target_name`, `tailscale_target_type`, `tailscale_target_property`, `tailscale_audit_old`, `tailscale_audit_new`, `tailscale_audit_details`, and `error` (on WARN records). |
| `tailscale.key.expiring` | When a key expires within the configured `expiry_warn` window. | **WARN**. | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description`, and `tailscale_key_expires_in_seconds` (seconds *until* expiry, a remaining duration — not an absolute timestamp). |
| `tailscale.webhook.<type>` | Per webhook event; `<type>` is the Tailscale event type. | **WARN** for types containing `Expir`, `Suspend`, `NeedsApproval`, or `Deleted`; otherwise INFO. | Webhook event payload fields, including `tailscale_webhook_type`. |
| `tailscale.device.posture` | Per device. **Gated** by `collect_posture`. | INFO. | Device identity plus posture/attribute fields. |

> The **`tailscale_node_hostname`** attribute on `tailscale.network.flow` is populated only when the
> node IP/ID could be resolved against the device-enrichment cache; otherwise the record carries the
> raw `tailscale_node_id`/addresses without a hostname.

---

## Node metrics scraper

The node metrics scraper (P3) is an **optional, gated** collector that scrapes the Prometheus
metrics endpoint exposed by `tailscaled` on one or more nodes and forwards them through the same
OTLP pipeline.

Key behavior:

- **Verbatim forwarding.** Each scraped `tailscaled` series is re-emitted with its **original
  metric name and original labels preserved** — these are *not* renamed into the curated
  `tailscale.*` namespace and are *not* subject to our semconv naming. (Grafana Cloud's standard
  OTLP→Prometheus normalization still applies on ingest.)
- **An added `instance` label.** Every forwarded series gains an `instance` label identifying the
  scraped node, so you can distinguish series across targets.
- **Instrument mapping.** Counters from the node are re-emitted as **deltas**; gauges are
  re-emitted as **gauges**.
- **Per-target up signal.** A `tailscale.node.up` gauge (→ `tailscale_node_up_ratio`) is emitted
  per target with the `instance` label, reporting whether the last scrape of that node succeeded.

Node identity is carried as **labels** (notably `instance`) on the forwarded series, **not** as
OTEL Resource attributes. This keeps the forwarded metrics queryable alongside the rest of the
fleet without needing resource-attribute joins.

---

## Cross-source de-duplication

Flow logs and audit events can arrive over more than one path at once — for example polling the API
**and** receiving the HEC stream (`source: both`), or running the webhook receiver alongside audit
polling. To avoid double-counting, the shared **audit** and **flow** processors carry an optional
**dedup set**: records already seen (keyed on their stable identity) are dropped before they reach
the metric counters and log emitters.

- Enabling `poll` + `stream` for a flow/audit collector is safe — the dedup set prevents the same
  record from being counted twice.
- `webhook` + `audit` de-duplication is **best-effort** (the two sources don't always share a
  perfectly stable key), so treat overlapping webhook/audit configurations as approximately, not
  exactly, deduplicated.

---

## Querying in Grafana

Default datasources: metrics → `grafanacloud-prom`, logs → `grafanacloud-logs`.

### PromQL (metrics)

Total network throughput (bytes/sec), summed across all dimensions:

```promql
sum(rate(tailscale_network_io_bytes_total[$__rate_interval]))
```

Throughput broken out by direction:

```promql
sum by (network_io_direction) (rate(tailscale_network_io_bytes_total[$__rate_interval]))
```

Number of devices currently online (filter the boolean gauge to `1`):

```promql
count(tailscale_device_online_ratio == 1)
```

Is the exporter up?

```promql
tailscale2otel_up_ratio
```

Devices whose node key expires within 7 days:

```promql
(tailscale_device_key_expiry_seconds - time()) < (7 * 24 * 3600)
```

Scrape error rate by collector:

```promql
sum by (tailscale_collector) (rate(tailscale2otel_scrape_errors_total[$__rate_interval]))
```

### LogQL (logs)

All audit events for the service:

```logql
{service_name="tailscale2otel"} | event_name="tailscale.config.audit"
```

Only audit events that were emitted at WARN (i.e. carried an error):

```logql
{service_name="tailscale2otel"} | event_name="tailscale.config.audit" | severity="WARN"
```

Per-connection flow records to a specific destination node:

```logql
{service_name="tailscale2otel"} | event_name="tailscale.network.flow" | tailscale_dst_node="my-host"
```
