---
title: Metrics & Signals
description: The as-built reference for every OpenTelemetry metric and log signal tailscale2otel emits, including OTLP→Prometheus naming
---

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

<!-- BEGIN GENERATED: metrics groups="Self-observability" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale2otel.admin.auth.rejected` | `1` | counter | `tailscale2otel_admin_auth_rejected_total` | `reason` | Admin HTTP requests rejected by the auth gate (status page + pprof), by reason. |
| `tailscale2otel.api.duration` | `s` | histogram | `tailscale2otel_api_duration_seconds` | `endpoint`, `http_response_status_code` | Tailscale API request wall-clock latency in seconds, by endpoint and HTTP status code. Covers the full logical request including any retry backoff (not just server time). Use the 429 status-code bucket here plus tailscale2otel.api.retries for rate-limit visibility — the Tailscale API exposes no rate-limit-remaining headers. |
| `tailscale2otel.api.requests` | `1` | counter | `tailscale2otel_api_requests_total` | `endpoint`, `http_response_status_code` | Tailscale API requests, by endpoint and HTTP status code. |
| `tailscale2otel.api.retries` | `1` | counter | `tailscale2otel_api_retries_total` | `endpoint` | API retry attempts, by endpoint. |
| `tailscale2otel.build_info` | `1` | gauge | `tailscale2otel_build_info_ratio` | `go_version` | Constant `1` build-info gauge; the Go runtime version is carried as the `go.version` label (the service version is promoted from the resource as `service_version`). |
| `tailscale2otel.checkpoint.persist.errors` | `1` | counter | `tailscale2otel_checkpoint_persist_errors_total` | `tailscale_collector` | Count of checkpoint-persistence failures, by collector (the window succeeded but its high-water mark could not be saved). |
| `tailscale2otel.component.errors` | `1` | counter | `tailscale2otel_component_errors_total` | `component` | Failures of non-collector subsystems (receivers, admin server, streaming auto-configure), by component. |
| `tailscale2otel.dedup.evictions` | `1` | counter | `tailscale2otel_dedup_evictions_total` | `dedup_set` | Keys evicted from a de-duplication set because it was at capacity, by set (sustained growth means the set is undersized). |
| `tailscale2otel.dedup.size` | `1` | gauge | `tailscale2otel_dedup_size_ratio` | `dedup_set` | Keys currently held in a cross-source de-duplication set, by set (a **count**, despite the `_ratio` suffix). |
| `tailscale2otel.enrich.cache_age` | `s` | gauge | `tailscale2otel_enrich_cache_age_seconds` | — | Age of the device-enrichment cache (since last refresh). |
| `tailscale2otel.enrich.cache_size` | `1` | gauge | `tailscale2otel_enrich_cache_size_ratio` | — | Number of devices in the enrichment cache (a **count**, despite `_ratio`). |
| `tailscale2otel.export.failures` | `1` | counter | `tailscale2otel_export_failures_total` | `error_type` | OTLP export failures, by error class. |
| `tailscale2otel.runtime.gc.count` | `1` | counter | `tailscale2otel_runtime_gc_count_total` | — | Completed garbage-collection cycles since process start. |
| `tailscale2otel.runtime.gc.cpu_fraction` | `1` | gauge | `tailscale2otel_runtime_gc_cpu_fraction_ratio` | — | Fraction of total CPU time used by the garbage collector since process start (0..1). |
| `tailscale2otel.runtime.gc.next_target` | `By` | gauge | `tailscale2otel_runtime_gc_next_target_bytes` | — | Target heap size (bytes) for the next garbage collection. |
| `tailscale2otel.runtime.gc.pause_time` | `s` | counter | `tailscale2otel_runtime_gc_pause_time_seconds_total` | — | Cumulative stop-the-world GC pause time since process start. |
| `tailscale2otel.runtime.gomaxprocs` | `1` | gauge | `tailscale2otel_runtime_gomaxprocs_ratio` | — | Current GOMAXPROCS, the max OS threads executing Go code (a **count**, despite the `_ratio` suffix). |
| `tailscale2otel.runtime.goroutines` | `1` | gauge | `tailscale2otel_runtime_goroutines_ratio` | — | Number of live goroutines (a **count**, despite the `_ratio` Prometheus suffix). |
| `tailscale2otel.runtime.memory.alloc` | `By` | counter | `tailscale2otel_runtime_memory_alloc_bytes_total` | — | Cumulative bytes allocated on the heap since process start (includes freed). |
| `tailscale2otel.runtime.memory.heap_alloc` | `By` | gauge | `tailscale2otel_runtime_memory_heap_alloc_bytes` | — | Bytes of allocated heap objects currently in use. |
| `tailscale2otel.runtime.memory.heap_inuse` | `By` | gauge | `tailscale2otel_runtime_memory_heap_inuse_bytes` | — | Bytes in in-use heap spans. |
| `tailscale2otel.runtime.memory.heap_objects` | `1` | gauge | `tailscale2otel_runtime_memory_heap_objects_ratio` | — | Number of live heap objects (a **count**, despite the `_ratio` suffix). |
| `tailscale2otel.runtime.memory.heap_sys` | `By` | gauge | `tailscale2otel_runtime_memory_heap_sys_bytes` | — | Bytes of heap memory obtained from the OS. |
| `tailscale2otel.runtime.memory.stack_inuse` | `By` | gauge | `tailscale2otel_runtime_memory_stack_inuse_bytes` | — | Bytes in in-use stack spans. |
| `tailscale2otel.runtime.memory.sys` | `By` | gauge | `tailscale2otel_runtime_memory_sys_bytes` | — | Total bytes of memory obtained from the OS (the process's Go memory footprint). |
| `tailscale2otel.scrape.budget` | `1` | gauge | `tailscale2otel_scrape_budget_ratio` | `tailscale_collector` | Last scrape duration as a fraction of the collector's poll interval (duration ÷ interval); values near or above `1` mean the scrape risks overrunning its interval. |
| `tailscale2otel.scrape.duration` | `s` | gauge | `tailscale2otel_scrape_duration_seconds` | `tailscale_collector` | Wall-clock duration of the last scrape, per collector. |
| `tailscale2otel.scrape.errors` | `1` | counter | `tailscale2otel_scrape_errors_total` | `tailscale_collector`, `error_type` | Count of scrape errors, by collector and error class. |
| `tailscale2otel.scrape.last_timestamp` | `s` | gauge | `tailscale2otel_scrape_last_timestamp_seconds` | `tailscale_collector` | Unix timestamp the last scrape *finished* (success **or** failure); pair with `scrape.success` to detect last-success staleness. |
| `tailscale2otel.scrape.staleness` | `s` | gauge | `tailscale2otel_scrape_staleness_seconds` | `tailscale_collector` | Seconds since this collector's last successful scrape (counts up from process start until the first success); pair with `scrape.success` for freshness alerting. |
| `tailscale2otel.scrape.success` | `1` | gauge | `tailscale2otel_scrape_success_ratio` | `tailscale_collector` | `1` if the last scrape for that collector succeeded, else `0`. |
| `tailscale2otel.series.active` | `{series}` | gauge | `tailscale2otel_series_active` | `metric_name` | Exact distinct active time series emitted for `metric.name` during the last export interval; bounded by a per-metric cap (the value pins at the cap when exceeded). A **count**. |
| `tailscale2otel.series.limit` | `{series}` | gauge | `tailscale2otel_series_limit` | — | Effective per-metric active-series cap (`cardinality.metric_limit`): the point at which excess series collapse into `otel_metric_overflow` (silent per-series loss). Emitted only when a positive limit is configured. A **count**. |
| `tailscale2otel.series.overflowing` | `1` | gauge | `tailscale2otel_series_overflowing_ratio` | `metric_name` | 1 when `metric.name` reached the per-metric series cap during the last interval (excess series silently dropped into `otel_metric_overflow`), else 0. Always 0 when no positive `cardinality.metric_limit` is configured. |
| `tailscale2otel.up` | `1` | gauge | `tailscale2otel_up_ratio` | — | Liveness flag: `1` while the service is running and reporting. |
| `tailscale2otel.update_available` | `1` | gauge | `tailscale2otel_update_available_ratio` | — | `1` when a newer tailscale2otel release is available on GitHub than the running build, else `0` (a **flag**, despite the `_ratio` Prometheus suffix). Emitted only when `version_checks.self` is enabled and both the running and latest versions parse — dev builds (version `dev`) never emit. Fail-open: a blocked/failed GitHub fetch emits nothing. |
<!-- END GENERATED -->

### Network / flow (`tailscale.network.*`, `tailscale.config.audit.*`)

Aggregated, low-cardinality counters derived from flow logs and audit logs. The full-fidelity
per-connection detail is emitted as **log records** (see [Log events](#log-events)).

<!-- BEGIN GENERATED: metrics groups="Network / flow" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.config.audit.changes` | `{event}` | counter | `tailscale_config_audit_changes_total` | `tailscale_audit_change`, `tailscale_audit_action`, `tailscale_actor_type` | Curated security- and lifecycle-relevant configuration-audit changes, by change category, action, and actor type. |
| `tailscale.config.audit.events` | `{event}` | counter | `tailscale_config_audit_events_total` | `tailscale_audit_action`, `tailscale_audit_origin` | Configuration-audit events, by action and origin. |
| `tailscale.network.flow.logs_dropped` | `{record}` | counter | `tailscale_network_flow_logs_dropped_total` | — | Flow LOG records suppressed by the per-window volume guard (collectors.flowlogs.max_log_records_per_window); 0 unless truncating. Metrics are never dropped, only logs. |
| `tailscale.network.flows` | `{flow}` | counter | `tailscale_network_flows_total` | `network_transport`, `tailscale_traffic_type` | Count of distinct flows observed (lower cardinality than network.io/packets). |
| `tailscale.network.io` | `By` | counter | `tailscale_network_io_bytes_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `source_port`, `destination_port`, `tailscale_dst_service` | Bytes transferred on the tailnet, by direction, transport, traffic type, and source/destination node. |
| `tailscale.network.io.rollup` | `By` | counter | `tailscale_network_io_rollup_bytes_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `tailscale_dst_service` | Bytes transferred on the tailnet, bounded top-N rollup: the busiest source/destination node pairs by total bytes are kept per flush and the remainder is folded into a tailscale.src.node/tailscale.dst.node="__other__" series per transport, traffic type, and destination service, so totals are preserved. Carries no L4 ports. Emitted when cardinality.flow.metrics_mode is rollup or both (the default). |
| `tailscale.network.packets` | `{packet}` | counter | `tailscale_network_packets_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `source_port`, `destination_port`, `tailscale_dst_service` | Packets transferred on the tailnet, with the same dimensions as network.io. |
| `tailscale.network.packets.rollup` | `{packet}` | counter | `tailscale_network_packets_rollup_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `tailscale_dst_service` | Packets transferred on the tailnet, with the same bounded top-N rollup dimensions as network.io.rollup. |
| `tailscale.network.unique.dst_peers` | `{peer}` | gauge | `tailscale_network_unique_dst_peers` | `tailscale_src_node` | Distinct destination nodes (peers) observed per source node in the last rollup flush interval (exact count, reset each flush). Emitted when cardinality.flow.metrics_mode is rollup or both and cardinality.flow.node_dims are on. |
| `tailscale.network.unique.dst_ports` | `{port}` | gauge | `tailscale_network_unique_dst_ports` | `tailscale_src_node` | Distinct destination ports observed per source node in the last rollup flush interval (exact count, reset each flush) — port-level visibility without per-port series. |
<!-- END GENERATED -->

> Label gating on `network.io`/`network.packets`: `tailscale_src_node`/`tailscale_dst_node` are
> gated by `cardinality.flow.node_dims` (**on** by default); `source_port`/`destination_port` are
> gated by `cardinality.flow.source_port` / `cardinality.flow.destination_port` (both **off** by
> default, as ports add cardinality).

> **Per-metric cardinality cap.** Every metric is bounded by `cardinality.metric_limit` (default
> 10000) — the OTLP SDK's hard limit on distinct series per instrument per export cycle. Series past
> it collapse into a single `{otel_metric_overflow="true"}` series (silent loss of per-series
> detail). So a label-less `tailscale_network_io_bytes_total{otel_metric_overflow="true"}` (or the
> same on `network.packets`) means you are **over the cap** — raise `metric_limit` or lower flow
> cardinality (ephemeral `source_port` is the biggest driver). `tailscale2otel.series.active` pins at
> the same cap, so it flags the condition too.

### Devices (`tailscale.device.*`, `tailscale.devices.count`)

Per-device gauges plus a fleet roll-up. "id dims" below is shorthand for the common device-identity
attribute set: `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`.

<!-- BEGIN GENERATED: metrics groups="Devices" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.derp.region.devices` | `1` | gauge | `tailscale_derp_region_devices_ratio` | `tailscale_derp_region` | Number of devices reporting latency to a DERP region (a **count**). **Gated** by `cardinality.derp_region_rollup`. |
| `tailscale.derp.region.latency_min` | `s` | gauge | `tailscale_derp_region_latency_min_seconds` | `tailscale_derp_region` | Best (minimum) device→DERP-region latency across the tailnet; one series per region. **Gated** by `cardinality.derp_region_rollup`. |
| `tailscale.derp.region.preferred` | `1` | gauge | `tailscale_derp_region_preferred_ratio` | `tailscale_derp_region` | Number of devices that prefer a DERP region (a **count**). **Gated** by `cardinality.derp_region_rollup`. |
| `tailscale.device.attribute` | `1` | gauge | `tailscale_device_attribute_ratio` | `host_name`, `host_id`, `attribute` | Numeric device posture attribute — boolean attributes as `0`/`1`, numeric attributes as their value (e.g. `intune:isEncrypted`, `custom:myScore`); one series per device per attribute, the namespaced posture key carried as the `attribute` label. **Gated** by `collect_posture` and the `attribute_namespaces` allow-list. |
| `tailscale.device.attribute.info` | `1` | gauge | `tailscale_device_attribute_info_ratio` | `host_name`, `host_id`, `attribute`, `value` | String/enum device posture attribute info gauge (constant `1`); the namespaced posture key is the `attribute` label and its string value the `value` label (e.g. `intune:complianceState`=`compliant`, `ip:country`=`GB`). **Gated** by `collect_posture` and the `attribute_namespaces` allow-list. |
| `tailscale.device.derp.latency` | `s` | gauge | `tailscale_device_derp_latency_seconds` | `host_name`, `host_id`, `tailscale_derp_region`, `tailscale_derp_preferred` | Latency from the device to a DERP region; one series per region. |
| `tailscale.device.key.expiry` | `s` | gauge | `tailscale_device_key_expiry_seconds` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | Unix timestamp the device node key expires. |
| `tailscale.device.last_seen` | `s` | gauge | `tailscale_device_last_seen_seconds` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | Unix timestamp the device was last seen. |
| `tailscale.device.online` | `1` | gauge | `tailscale_device_online_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if the device is currently online, else `0`. |
| `tailscale.device.posture` | `1` | gauge | `tailscale_device_posture_ratio` | `host_name`, `host_id`, `os`, `os_version`, `ts_version`, `auto_update`, `encrypted`, `track` | Per-device posture info gauge (constant `1`); device security posture — OS, Tailscale client version, auto-update, state-encrypted, release track — carried as labels. **Gated** by `collect_posture`. |
| `tailscale.device.routes.advertised` | `{route}` | gauge | `tailscale_device_routes_advertised` | `host_name`, `host_id` | Number of subnet routes the device advertises. **Gated** by `collect_routes`. |
| `tailscale.device.routes.enabled` | `{route}` | gauge | `tailscale_device_routes_enabled` | `host_name`, `host_id` | Number of advertised routes that are enabled/approved. **Gated** by `collect_routes`. |
| `tailscale.device.update_available` | `1` | gauge | `tailscale_device_update_available_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if a Tailscale client update is available for the device. |
| `tailscale.device.version_skew` | `1` | gauge | `tailscale_device_version_skew_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | Minor releases this device's Tailscale client is behind the latest stable (`latest.minor − device.minor`, same major, clamped ≥0; patch-only drift is 0 — see `tailscale.device.update_available` for that). Per-device, gated by `cardinality.per_entity.device`. Emitted only when `version_checks.devices` is enabled, the upstream latest is known, and the device version parses. |
| `tailscale.device_invites.count` | `1` | gauge | `tailscale_device_invites_count_ratio` | `tailscale_device_invite_accepted`, `tailscale_device_invite_allow_exit_node`, `tailscale_device_invite_multi_use` | Device-share invites (accepted and pending) (a **count**, despite `_ratio`), bucketed by accepted/pending and the exit-node / multi-use exposure flags. **Gated** by `collect_device_invites` (one API call per device). |
| `tailscale.devices.by_tag` | `1` | gauge | `tailscale_devices_by_tag_ratio` | `tailscale_tag` | Device count per ACL tag (a device with N tags counts in N series). **Gated** by `collect_tag_rollup`; capped by `tag_rollup_limit` with overflow tags folded into `tailscale.tag="__other__"`. |
| `tailscale.devices.by_version` | `1` | gauge | `tailscale_devices_by_version_ratio` | `tailscale_client_version` | Device count per normalized Tailscale client version (`major.minor.patch`; unparseable→`unknown`); one series per version. Devices with no reported version (external) are excluded. |
| `tailscale.devices.count` | `1` | gauge | `tailscale_devices_count_ratio` | `os_type`, `tailscale_authorized`, `tailscale_external` | Fleet device count (a **count**, despite `_ratio`), bucketed by OS/authorized/external. |
| `tailscale.devices.ephemeral` | `1` | gauge | `tailscale_devices_ephemeral_ratio` | — | Number of ephemeral devices in the tailnet (a **count**, despite `_ratio`). |
| `tailscale.devices.key_expiry` | `d` | histogram | `tailscale_devices_key_expiry_days` | — | Distribution of days until each device's node key expires (negative = already expired; the `(-inf,0]` bucket). Excludes devices with key expiry disabled. Buckets (days): 0, 7, 30, 90, 180, 365. |
| `tailscale.devices.outdated` | `1` | gauge | `tailscale_devices_outdated_ratio` | — | Number of devices at least `version_checks.devices.outdated_minor_threshold` minor releases behind the latest Tailscale stable (a **count**, despite `_ratio`). Fleet-wide, no labels. Emitted only when `version_checks.devices` is enabled and the upstream latest is known. |
| `tailscale.devices.untagged` | `1` | gauge | `tailscale_devices_untagged_ratio` | — | Number of non-external devices with no ACL tags (a **count**, despite `_ratio`); a tagging-hygiene signal. External (shared-in) devices are excluded — they can't be tagged by this tailnet. |
| `tailscale.fleet.latest_version` | `1` | gauge | `tailscale_fleet_latest_version_ratio` | `tailscale_client_version` | Always `1`; an info gauge whose `tailscale.client_version` label carries the latest Tailscale stable client version (`major.minor.patch`) as fetched from pkgs.tailscale.com. Emitted only when `version_checks.devices` is enabled and the upstream fetch has succeeded. |
| `tailscale.tailnet_lock.errors` | `1` | gauge | `tailscale_tailnet_lock_errors_ratio` | — | Number of devices with a non-empty tailnet-lock error (a **count**, despite `_ratio`); the only actionable tailnet-lock signal the API exposes (every node carries a lock key regardless of whether tailnet lock is enabled). |
<!-- END GENERATED -->

### Users (`tailscale.users.count`, `tailscale.user.*`, `tailscale.user_invites.count`)

User roll-ups and per-user gauges. Per-user "id dims" = `enduser_id`, `tailscale_user_login`.

<!-- BEGIN GENERATED: metrics groups="Users" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.user.connected` | `1` | gauge | `tailscale_user_connected_ratio` | `enduser_id`, `tailscale_user_login` | `1` if the user is currently connected, else `0`. |
| `tailscale.user.devices` | `1` | gauge | `tailscale_user_devices_ratio` | `enduser_id`, `tailscale_user_login` | Number of devices owned by the user (a **count**). |
| `tailscale.user.last_seen` | `s` | gauge | `tailscale_user_last_seen_seconds` | `enduser_id`, `tailscale_user_login` | Unix timestamp the user was last seen. |
| `tailscale.user_invites.count` | `1` | gauge | `tailscale_user_invites_count_ratio` | `tailscale_user_invite_role`, `tailscale_user_invite_accepted` | Outstanding/processed user invites (a **count**), by role and accepted flag. |
| `tailscale.users.count` | `1` | gauge | `tailscale_users_count_ratio` | `tailscale_user_role`, `tailscale_user_status`, `tailscale_user_type` | User count (a **count**), bucketed by role/status/type. |
<!-- END GENERATED -->

### Keys (`tailscale.key.*`, `tailscale.keys.count`)

<!-- BEGIN GENERATED: metrics groups="Keys" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.key.expiry` | `s` | gauge | `tailscale_key_expiry_seconds` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_auth_kind`, `tailscale_key_description` | Unix timestamp a Tailscale key expires; one series per key. |
| `tailscale.key.preauthorized` | `1` | gauge | `tailscale_key_preauthorized_ratio` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description` | Whether an auth key is preauthorized (1) or not (0); one series per auth key. Gated by `cardinality.per_entity.key`. |
| `tailscale.key.scopes` | `1` | gauge | `tailscale_key_scopes_ratio` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description` | Number of OAuth scopes granted to a credential (scope-sprawl signal); one series per OAuth-client/API credential. Gated by `cardinality.per_entity.key`. |
| `tailscale.keys.count` | `1` | gauge | `tailscale_keys_count_ratio` | `tailscale_key_type`, `tailscale_key_auth_kind`, `tailscale_key_revoked`, `tailscale_key_invalid` | Key count (a **count**), bucketed by type/auth_kind/revoked/invalid. |
<!-- END GENERATED -->

> Per-entity gauge gating: the per-device, per-user, and per-key gauges above are gated by
> `cardinality.per_entity.device` / `cardinality.per_entity.user` / `cardinality.per_entity.key` (all **on** by default).
> Set one to `false` to drop that collector's per-entity series and keep only its aggregate
> `*.count` roll-up; the key-expiry **warning log** still fires regardless.

### Settings / ACL / DNS (`tailscale.setting.*`, `tailscale.acl.*`, `tailscale.dns.*`)

<!-- BEGIN GENERATED: metrics groups="Settings,ACL,DNS" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.acl.autoapprovers` | `1` | gauge | `tailscale_acl_autoapprovers_ratio` | `tailscale_acl_autoapprover_kind` | Number of auto-approver entries by kind (routes, exit_node, services) (a **count**, despite `_ratio`). |
| `tailscale.acl.last_changed` | `s` | gauge | `tailscale_acl_last_changed_seconds` | — | Unix timestamp the ACL policy last changed (detected by ETag). |
| `tailscale.acl.posture_gated_rules` | `1` | gauge | `tailscale_acl_posture_gated_rules_ratio` | `tailscale_acl_section` | Number of rules gated by a device-posture condition (`srcPosture`), per section (a **count**, despite `_ratio`). |
| `tailscale.acl.rules` | `1` | gauge | `tailscale_acl_rules_ratio` | `tailscale_acl_section` | Number of rules per ACL section (a **count**, despite `_ratio`). |
| `tailscale.acl.size` | `By` | gauge | `tailscale_acl_size_bytes` | — | Size of the current ACL policy document, in bytes. |
| `tailscale.acl.ssh_wildcard` | `1` | gauge | `tailscale_acl_ssh_wildcard_ratio` | — | Number of Tailscale SSH rules with a wildcard (`*`) source or destination (a **count**, despite `_ratio`). |
| `tailscale.acl.unrestricted_rules` | `1` | gauge | `tailscale_acl_unrestricted_rules_ratio` | `tailscale_acl_section` | Number of non-deny rules matching any source to any destination (wildcard `src` and `dst`), per section (a **count**, despite `_ratio`). |
| `tailscale.acl.wildcard_rules` | `1` | gauge | `tailscale_acl_wildcard_rules_ratio` | `tailscale_acl_section`, `tailscale_acl_position` | Number of non-deny ACL/grant rules with a wildcard (`*`) source or destination, per section and position (a **count**, despite `_ratio`). |
| `tailscale.dns.magic_dns` | `1` | gauge | `tailscale_dns_magic_dns_ratio` | — | `1` if MagicDNS is enabled, else `0`. |
| `tailscale.dns.nameservers.count` | `1` | gauge | `tailscale_dns_nameservers_count_ratio` | — | Number of configured nameservers (a **count**). |
| `tailscale.dns.override_local` | `1` | gauge | `tailscale_dns_override_local_ratio` | — | `1` if Tailscale DNS resolvers override the local OS DNS configuration (`preferences.overrideLocalDNS`), else `0`. |
| `tailscale.dns.resolver` | `1` | gauge | `tailscale_dns_resolver_ratio` | `tailscale_dns_resolver_address`, `tailscale_dns_resolver_kind`, `tailscale_dns_resolver_domain`, `tailscale_dns_resolver_use_with_exit_node` | Info gauge (always `1`) for each configured DNS resolver, labeled by `address`, `kind` (`global`\|`split`), split-DNS `domain` (empty for global), and `use_with_exit_node`. |
| `tailscale.dns.resolvers.use_with_exit_node` | `1` | gauge | `tailscale_dns_resolvers_use_with_exit_node_ratio` | — | Number of DNS resolvers (global + split-DNS) set to remain in use under an exit node (`useWithExitNode`, Tailscale v1.88.1+; a **count**). |
| `tailscale.dns.search_paths.count` | `1` | gauge | `tailscale_dns_search_paths_count_ratio` | — | Number of DNS search paths (a **count**). |
| `tailscale.dns.split_zones.count` | `1` | gauge | `tailscale_dns_split_zones_count_ratio` | — | Number of split-DNS zones configured (a **count**). |
| `tailscale.setting.devices_key_duration` | `d` | gauge | `tailscale_setting_devices_key_duration_days` | — | Configured device key expiry duration, in days. |
| `tailscale.setting.enabled` | `1` | gauge | `tailscale_setting_enabled_ratio` | `tailscale_setting_name` | `1` if the named tailnet setting is enabled, else `0`. |
| `tailscale.setting.users_external_tailnets_role` | `1` | gauge | `tailscale_setting_users_external_tailnets_role_ratio` | `tailscale_setting_role` | Info gauge (constant `1`); the user role allowed to join external tailnets, carried as the `tailscale.setting.role` label. |
<!-- END GENERATED -->

### Contacts (`tailscale.contact.*`)

Tailnet contact verification status. The contact **email is never emitted** (PII); only whether each
contact type (`account`/`support`/`security`) still needs verification — an unverified `security`
contact is worth alerting on.

<!-- BEGIN GENERATED: metrics groups="Contacts" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.contact.needs_verification` | `1` | gauge | `tailscale_contact_needs_verification_ratio` | `tailscale_contact_type` | `1` if the tailnet contact email still needs verification, else `0`; one series per contact type (`account`/`support`/`security`). The email address is never emitted. |
<!-- END GENERATED -->

### Webhook endpoints (`tailscale.webhook_endpoint*.*`)

Inventory of configured webhook **endpoints** (where Tailscale posts event notifications) — distinct
from the [stream/webhook receiver](#receivers-stream-webhook-tailscalestream-tailscalewebhook) metrics. Endpoint URL, secret and
creator are **never emitted**. The per-endpoint subscriptions gauge is gated by
`cardinality.per_entity.webhook`.

<!-- BEGIN GENERATED: metrics groups="Webhooks" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.webhook_endpoint.subscriptions` | `1` | gauge | `tailscale_webhook_endpoint_subscriptions_ratio` | `tailscale_webhook_endpoint_id`, `tailscale_webhook_endpoint_provider` | Number of event categories a webhook endpoint is subscribed to (a **count**); one series per endpoint. **Gated** by `cardinality.per_entity.webhook`. The endpoint URL/secret/creator are never emitted. |
| `tailscale.webhook_endpoints.count` | `1` | gauge | `tailscale_webhook_endpoints_count_ratio` | — | Number of configured webhook endpoints (a **count**, despite `_ratio`). |
<!-- END GENERATED -->

### Posture integrations (`tailscale.posture_integration*.*`)

Device-posture provider integrations (MDM/EDR such as Intune) and their sync health. Alert on
`tailscale.posture_integration.last_sync` going stale. Provider identifiers
(`clientId`/`tenantId`/`cloudId`) are never emitted.

<!-- BEGIN GENERATED: metrics groups="Posture" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.posture_integration.last_sync` | `s` | gauge | `tailscale_posture_integration_last_sync_seconds` | `tailscale_posture_provider`, `tailscale_posture_integration` | Unix timestamp of the integration's last successful sync (alert on staleness). Emitted only once a sync has occurred. |
| `tailscale.posture_integration.matched` | `1` | gauge | `tailscale_posture_integration_matched_ratio` | `tailscale_posture_provider`, `tailscale_posture_integration` | Devices matched to a provider host by the posture integration (a **count**); one series per provider/integration. |
| `tailscale.posture_integration.possible_matched` | `1` | gauge | `tailscale_posture_integration_possible_matched_ratio` | `tailscale_posture_provider`, `tailscale_posture_integration` | Devices that could potentially be matched by the posture integration (a **count**). |
| `tailscale.posture_integration.provider_hosts` | `1` | gauge | `tailscale_posture_integration_provider_hosts_ratio` | `tailscale_posture_provider`, `tailscale_posture_integration` | Hosts known to the posture provider (a **count**). |
| `tailscale.posture_integrations.count` | `1` | gauge | `tailscale_posture_integrations_count_ratio` | — | Number of configured device-posture integrations (a **count**, despite `_ratio`). |
<!-- END GENERATED -->

### Log streaming health (`tailscale.logstream.*`)

Tailscale's own view of whether it is successfully delivering your **configuration** and **network**
logs to a configured SIEM sink (a meta-signal, independent of the flow/audit collectors). The
cumulative counters are emitted as **deltas** (use `rate()`). On a tailnet with no SIEM sink the
status endpoint returns 4xx/empty → `tailscale.logstream.configured` = 0 and no health series (no
error noise). The error **text** is on the `tailscale.logstream.error` log event, never a metric label.

<!-- BEGIN GENERATED: metrics groups="Log streaming" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.logstream.bytes_sent` | `By` | counter | `tailscale_logstream_bytes_sent_bytes_total` | `tailscale_logstream_type` | Bytes delivered to the log-stream sink (emitted as the delta of Tailscale's cumulative counter). |
| `tailscale.logstream.configured` | `1` | gauge | `tailscale_logstream_configured_ratio` | `tailscale_logstream_type` | `1` if a log stream is configured for this log type, else `0`. |
| `tailscale.logstream.entries_sent` | `{event}` | counter | `tailscale_logstream_entries_sent_total` | `tailscale_logstream_type` | Log entries delivered to the sink. |
| `tailscale.logstream.error` | `1` | gauge | `tailscale_logstream_error_ratio` | `tailscale_logstream_type` | `1` if the last delivery reported an error, else `0`. The error text is on the `tailscale.logstream.error` LOG event, never a label. |
| `tailscale.logstream.last_activity` | `s` | gauge | `tailscale_logstream_last_activity_seconds` | `tailscale_logstream_type` | Unix timestamp of the most recent delivery activity (alert on staleness). |
| `tailscale.logstream.max_body_requests` | `{request}` | counter | `tailscale_logstream_max_body_requests_total` | `tailscale_logstream_type` | Delivery requests that hit the maximum body size (a SIEM backpressure signal). |
| `tailscale.logstream.requests` | `{request}` | counter | `tailscale_logstream_requests_total` | `tailscale_logstream_type` | Total delivery requests to the sink. |
| `tailscale.logstream.requests_failed` | `{request}` | counter | `tailscale_logstream_requests_failed_total` | `tailscale_logstream_type` | Failed delivery requests to the sink (alert on a sustained rate). |
| `tailscale.logstream.spoofed_entries` | `{event}` | counter | `tailscale_logstream_spoofed_entries_total` | `tailscale_logstream_type` | Log entries rejected as spoofed. |
<!-- END GENERATED -->

### Tailscale Services / VIP (`tailscale.service*.*`)

Tailscale Services (VIP services) and their backing hosts. Service addresses, comments and
annotations are **never emitted**. The per-service `ports`/`hosts` gauges are gated by
`cardinality.per_entity.service`; `hosts` additionally requires `collect_hosts` (one extra API call
per service).

<!-- BEGIN GENERATED: metrics groups="Services" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.service.hosts` | `1` | gauge | `tailscale_service_hosts_ratio` | `tailscale_service_name`, `tailscale_service_approval`, `tailscale_service_configured` | Backing-host **count** for a Tailscale Service, bucketed by approval + configured state; one series per service/approval/configured. **Gated** by `collect_hosts` (N+1 calls) and `cardinality.per_entity.service`. |
| `tailscale.service.ports` | `{port}` | gauge | `tailscale_service_ports` | `tailscale_service_name` | Number of port rules exposed by a Tailscale Service; one series per service. **Gated** by `cardinality.per_entity.service`. |
| `tailscale.services.count` | `1` | gauge | `tailscale_services_count_ratio` | — | Number of Tailscale Services (VIP services) in the tailnet (a **count**, despite `_ratio`). |
<!-- END GENERATED -->

### Features (`tailscale.feature.*`)

<!-- BEGIN GENERATED: metrics groups="Features" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.feature.enabled` | `1` | gauge | `tailscale_feature_enabled_ratio` | `tailscale_feature` | `1` if the named tailnet feature is enabled, else `0`; one series per feature. |
<!-- END GENERATED -->

> `tailscale.feature.enabled` for network-flow-logging is emitted in **both** ingestion modes: the
> flowlogs poller emits it directly when polling, and under `source: stream` a lightweight feature
> probe emits it on the flowlogs interval — so the signal is never lost when only the receiver runs.

### Receivers — stream & webhook (`tailscale.stream.*`, `tailscale.webhook.*`)

Health/throughput counters for the optional HEC log-stream receiver and the webhook receiver.

<!-- BEGIN GENERATED: metrics groups="Receivers" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.stream.decode_errors` | `{record}` | counter | `tailscale_stream_decode_errors_total` | `type` | Records that classified as a known type but failed to decode, by stream type (`flow`/`audit`). |
| `tailscale.stream.records` | `{record}` | counter | `tailscale_stream_records_total` | `type` | Records accepted by the HEC stream receiver, by stream type (`flow`/`audit`). |
| `tailscale.stream.rejected` | `{rejection}` | counter | `tailscale_stream_rejected_total` | `reason` | Records rejected by the stream receiver, by reason (`auth`/`unparsable`/`too_large`). |
| `tailscale.webhook.events` | `{event}` | counter | `tailscale_webhook_events_total` | `tailscale_webhook_type` | Webhook events accepted, by Tailscale event type. |
| `tailscale.webhook.rejected` | `1` | counter | `tailscale_webhook_rejected_total` | `reason` | Webhook deliveries rejected (e.g. bad HMAC), by reason. |
<!-- END GENERATED -->

### Node metrics scraper (`tailscale.node.*` + forwarded series)

The scraper emits one curated metric — the per-target health gauge below — and otherwise forwards
every scraped `tailscaled` series **verbatim**. Those forwarded series are runtime-named and are
**not** part of the curated catalog; see the dedicated
[Node metrics scraper](#node-metrics-scraper) section for the forwarding behavior and setup.

<!-- BEGIN GENERATED: metrics groups="Node metrics" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.node.up` | `1` | gauge | `tailscale_node_up_ratio` | `tailscale_node` | Per-target scrape health: `1` if the last scrape of that node succeeded, else `0`. |
| `tailscale2otel.nodemetrics.discovery.success` | `1` | gauge | `tailscale2otel_nodemetrics_discovery_success_ratio` | — | 1 if the last dynamic target-discovery refresh succeeded, else 0. Emitted only when discovery is enabled. |
| `tailscale2otel.nodemetrics.discovery.targets` | `{target}` | gauge | `tailscale2otel_nodemetrics_discovery_targets` | — | Active node-metrics scrape targets after the last refresh (static plus discovered). Emitted only when discovery is enabled. |
<!-- END GENERATED -->

### Reverse DNS (`tailscale.rdns.*`)

Self-observability for the reverse-DNS (PTR) enrichment cache (`enrichment.reverse_dns`). Emitted
only when `self_observability.enabled` is true; the admin status page shows the same figures directly
from the cache regardless. `queries` is the load placed on the upstream resolver and should stay low
relative to `lookups`; a non-zero `overflows` rate means `max_entries` is too small.

<!-- BEGIN GENERATED: metrics groups="Reverse DNS" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.rdns.cache.capacity` | `1` | gauge | `tailscale_rdns_cache_capacity_ratio` | — | Configured maximum number of entries (enrichment.reverse_dns.max_entries). |
| `tailscale.rdns.cache.entries` | `1` | gauge | `tailscale_rdns_cache_entries_ratio` | — | Current number of entries in the reverse-DNS cache (positive and negative). |
| `tailscale.rdns.cache.evictions` | `1` | counter | `tailscale_rdns_cache_evictions_total` | `reason` | Cache entries removed, by reason: expired (swept after their TTL) or purge (manual purge via the admin endpoint). |
| `tailscale.rdns.cache.lookups` | `1` | counter | `tailscale_rdns_cache_lookups_total` | `result` | Reverse-DNS cache hot-path lookups by result: hit (cached PTR name), negative (cached failed lookup), or miss (no cached entry; a background resolution is scheduled). |
| `tailscale.rdns.cache.overflows` | `1` | counter | `tailscale_rdns_cache_overflows_total` | — | Hot-path misses for new addresses that could not be scheduled because the cache was at enrichment.reverse_dns.max_entries. A non-zero rate means the cache is too small. |
| `tailscale.rdns.queries` | `1` | counter | `tailscale_rdns_queries_total` | `result` | Background PTR resolutions sent to the upstream resolver, by result (success or failure). This is the load the cache places on the resolver — it should stay low relative to lookups. |
<!-- END GENERATED -->

---

## Log events

Structured OTEL log records. They are exported via OTLP and land in **Loki** under datasource uid
`grafanacloud-logs`, all tagged with the label `service_name="tailscale2otel"`.

The OTEL event type is carried in the native log-record **`EventName`** field (set via the log
SDK's `SetEventName`, log v0.20.0+ — not a separate `event.name` attribute). Grafana Cloud's
OTLP→Loki ingestion exposes it as **`event_name`**, so you filter on `event_name` in LogQL (e.g.
`| event_name="tailscale.config.audit"`); the value keeps its dots. *Verified live against Grafana
Cloud:* the native `EventName` produces the same `event_name` key the earlier `event.name` attribute
did, so existing queries and the bundled dashboards are unaffected by the S4-1 migration.

<!-- BEGIN GENERATED: logs -->
| Event name | Severity | Key attributes | Description |
|---|---|---|---|
| `tailscale.config.audit` | INFO | `tailscale_audit_action`, `tailscale_audit_origin`, `tailscale_audit_event_group_id`, `enduser_id`, `tailscale_actor_login`, `tailscale_actor_display`, `tailscale_actor_type`, `tailscale_target_id`, `tailscale_target_name`, `tailscale_target_type`, `tailscale_target_property`, `tailscale_audit_old`, `tailscale_audit_new`, `tailscale_audit_details`, `error` | Per configuration-audit event: actor, target, action, and (when present) the before/after change. Emitted at **WARN** when the event carries an error, otherwise INFO. |
| `tailscale.device.posture` | INFO | `host_name`, `host_id` | Per-device posture/identity snapshot, carrying the device identity plus the posture attributes reported by the API. **Gated** by `collect_posture`; by default emitted only when a device's posture changes (see `posture_log_mode`). |
| `tailscale.device.tailnet_lock_error` | ERROR | `host_name`, `host_id` | Emitted per device when its tailnet-lock error is non-empty (e.g. an unsigned node); the error text is the log body. |
| `tailscale.key.expiring` | WARN | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_auth_kind`, `tailscale_key_description`, `tailscale_key_expires_in_seconds` | Emitted when a key expires within the configured `expiry_warn` window. Carries `tailscale.key.expires_in_seconds` (seconds *until* expiry, a remaining duration — not an absolute timestamp). |
| `tailscale.logstream.error` | ERROR | `tailscale_logstream_type` | Emitted when a log stream's last delivery reported an error; the error text is the log body. |
| `tailscale.network.flow` | INFO | `source_address`, `source_port`, `destination_address`, `destination_port`, `network_transport`, `network_type`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `tailscale_dst_service`, `tailscale_node_id`, `tailscale_node_hostname`, `tailscale_connections`, `tailscale_tx_bytes`, `tailscale_rx_bytes`, `tailscale_tx_packets`, `tailscale_rx_packets` | Per-connection (per_connection) or per-record (per_record) network-flow detail: the 5-tuple, transport, traffic type, source/destination node, and tx/rx bytes & packets. |
| `tailscale.webhook.<type>` | INFO / WARN by type | `tailscale_webhook_type`, `tailscale_tailnet` | Per webhook event; `<type>` is the Tailscale event type. Emitted at **WARN** for attention-worthy types (node key expiry, needs-approval/authorization/signature, deletions), otherwise INFO. The client-misconfig health events `exitNodeIPForwardingNotEnabled`/`subnetIPForwardingNotEnabled` are INFO and surfaced via the `NodeIPForwardingMisconfigured` alert. |
<!-- END GENERATED -->

> The **`tailscale_node_hostname`** attribute on `tailscale.network.flow` is populated only when the
> node IP/ID could be resolved against the device-enrichment cache; otherwise the record carries the
> raw `tailscale_node_id`/addresses without a hostname.

> **Device posture — metric vs. log.** Posture is exposed two ways. The **metric**
> `tailscale.device.posture` (→ `tailscale_device_posture_ratio`, a constant-`1` info gauge, one
> series per device) carries a curated, low-cardinality label set (`os`, `os_version`, `ts_version`,
> `auto_update`, `encrypted`, `track`) and is emitted **every scrape** — use it for fleet analytics
> (version skew, auto-update/encryption coverage, release-track outliers). The **log**
> `tailscale.device.posture` carries the full raw posture attribute set and, by default
> (`posture_log_mode: changes`), is emitted only when a device's posture **changes** — a full
> baseline dump on the first scrape after start, then per-device deltas — so it reads as an audit
> trail rather than a per-minute snapshot. Note that the device's own OS is `node_os` / `node_osVersion`
> (and the metric's `os` / `os_version` labels); the resource-level `os_type` / `os_description` on
> any signal describe the **collector** host, not the device.

> **Device posture attributes as metrics (MDM/identity integrations).** Beyond the curated
> `tailscale.device.posture` gauge above, the allow-listed posture-attribute namespaces (default:
> `intune`, `jamf`, `kandji`, `crowdstrike`, `sentinelone`, `kolide`, `ip` — see
> `collectors.devices.attribute_namespaces`) are promoted to two metrics, reusing the same per-device
> attribute fetch (no extra API calls; both **gated** by `collect_posture`). Each attribute lands in
> exactly one, by value type: booleans/numbers become **`tailscale_device_attribute_ratio`** (the value
> carries meaning — `0`/`1` for booleans, the number otherwise), and strings/enums become
> **`tailscale_device_attribute_info_ratio`** (constant `1`, the value carried in the `value` label).
> So `avg(tailscale_device_attribute_ratio{attribute="intune:isEncrypted"})` is the encrypted-fleet
> fraction, `tailscale_device_attribute_ratio{attribute="intune:isEncrypted"} == 0` finds unencrypted
> devices, and `count by(value)(tailscale_device_attribute_info_ratio{attribute="intune:complianceState"})`
> breaks the fleet down by compliance state. Series count ≈ devices × allow-listed attributes present
> (bounded for enum/bool); `node:*` is omitted from the default (already on the curated posture gauge)
> and `custom:*` is excluded by default since its values are operator-defined. Set
> `attribute_namespaces: ["*"]` to promote every namespace, or `[]` to disable.

---

## Node metrics scraper

The node metrics scraper (P3) is an **optional, gated** collector that scrapes the Prometheus
metrics endpoint exposed by `tailscaled` on one or more nodes and forwards them through the same
OTLP pipeline. For how to expose those endpoints on each node (enabling `--webclient`, the `:5252`
port, the required ACL grant, and per-target auth/TLS), see
[How to expose `tailscaled` metrics](./node-metrics.md).

Key behavior:

- **Verbatim forwarding.** Each scraped `tailscaled` series is re-emitted with its **original
  metric name and original labels preserved** — these are *not* renamed into the curated
  `tailscale.*` namespace and are *not* subject to our semconv naming. (Grafana Cloud's standard
  OTLP→Prometheus normalization still applies on ingest.)
- **An added `tailscale_node` label.** Every forwarded series gains a `tailscale_node` label
  (OTEL attribute `tailscale.node`) identifying the scraped node, so you can distinguish series
  across targets. It is deliberately **not** called `instance`: on Grafana Cloud the OTLP→Prometheus
  translation promotes the exporter's `service.instance.id` resource attribute to the `instance`
  label, which would overwrite a per-node `instance` and collapse every node's series onto the
  collector host.
- **Instrument mapping.** Counters from the node are re-emitted as **deltas**; gauges are
  re-emitted as **gauges**.
- **Per-target up signal.** A `tailscale.node.up` gauge (→ `tailscale_node_up_ratio`) is emitted
  per target with the `tailscale_node` label, reporting whether the last scrape of that node succeeded.
- **Cardinality controls (optional).** `collectors.node_metrics.metric_allow` / `metric_deny`
  (anchored regexes on the forwarded metric **name**, allow-then-deny) and `drop_labels` (label keys
  stripped from every forwarded series) trim the verbatim stream. They never affect
  `tailscale.node.up` or the `tailscale2otel.nodemetrics.discovery.*` gauges, and the `tailscale_node`
  label is never dropped. The scraper also enforces per-target `max_response_bytes` / `max_samples`
  limits, while dynamic discovery is bounded by `discovery.max_targets`.

Node identity is carried as **labels** (notably `tailscale_node`) on the forwarded series, **not** as
OTEL Resource attributes. This keeps the forwarded metrics queryable alongside the rest of the
fleet without needing resource-attribute joins.

---

## Cross-source de-duplication (a failsafe — pick one method)

**Choose ONE ingestion method per log type.** For flow and audit logs, run *either* the poller
(`source: poll`) *or* the HEC stream receiver (`source: stream`) — not both. Running both
(`source: both`, or `streaming.enabled` while a collector still polls) means the same data can
arrive twice; the exporter logs a **WARN at startup** when it detects this.

When data does arrive over more than one path, the shared **audit** and **flow** processors carry a
**dedup set** that drops already-seen records (keyed on their stable identity) before the metric
counters and log emitters. This is a **best-effort FAILSAFE, not a guarantee** — do not rely on it
as a supported mode:

- **Flow** poll↔stream de-dup is reliable: the key is the connection tuple
  (`nodeId|start|end|proto|src|dst`), identical across both sources.
- **Audit** poll↔stream de-dup keys on the event identity `eventGroupID|action|target.id|property`
  (time-free, because a streamed audit record has no inner `eventTime` and is timed from the HEC
  envelope — its millisecond timestamp never matches the API's nanosecond `eventTime`). This is
  reliable in practice but theoretical edge cases exist, hence "failsafe".
- `webhook` + `audit` de-duplication is **best-effort** on a normalized `(verb, subject, time-bucket)`
  key (the two sources don't always share a perfectly stable key), so treat overlapping
  webhook/audit configurations as approximately, not exactly, deduplicated.

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
