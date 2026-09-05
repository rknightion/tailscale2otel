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
`rate()`/`increase()`); **gauge** = point-in-time value; **histogram** = a distribution with
explicit buckets (rendered as `_bucket`/`_sum`/`_count` in Prometheus — never `_total`, and never
`_ratio` even at unit `1`); **updowncounter** = a non-monotonic sum (rendered without a `_total`
suffix, unlike a counter).

> **Universal attributes (every metric).** In addition to the per-metric attributes listed below,
> every metric data point carries `tailscale.tailnet` (`tailscale_tailnet` — the tailnet name;
> omitted on process-global self-obs series and under Headscale) and `tailscale2otel.provider`
> (`tailscale2otel_provider` — `tailscale` or `headscale`). These are **real labels on every backend**
> — Grafana Cloud, the opt-in Prometheus `/metrics` pull endpoint, and self-managed Mimir/Prometheus —
> so you can filter/group by tailnet with a direct matcher (e.g. `{tailscale_tailnet="example.com"}`),
> **no `target_info` join required**. Log records and trace spans carry the same two attributes.

### Self-observability (`tailscale2otel.*`)

Emitted by the service about itself. Use these for health, scrape success, API behavior, and
exporter health.

<!-- BEGIN GENERATED: metrics groups="Self-observability" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `process.cpu.time` | `s` | counter | `process_cpu_time_seconds_total` | `cpu_mode` | Cumulative process CPU time in seconds, by mode (`cpu.mode`=user\|system), read from getrusage(RUSAGE_SELF). Emitted on unix platforms only. |
| `process.uptime` | `s` | gauge | `process_uptime_seconds` | — | Seconds since the process started (wall-clock uptime). |
| `tailscale2otel.admin.auth.rejected` | `1` | counter | `tailscale2otel_admin_auth_rejected_total` | `reason` | Admin HTTP requests rejected by the auth gate (status page + pprof), by reason. |
| `tailscale2otel.annotation.degraded` | `1` | gauge | `tailscale2otel_annotation_degraded_ratio` | — | Whether the most recent Grafana annotation write failed and has not been recovered by a later success (`1` degraded, `0` healthy). The counterpart to `tailscale2otel.annotation.dropped`: the counter says how much was lost, this says whether it is still happening. |
| `tailscale2otel.annotation.dropped` | `{annotation}` | counter | `tailscale2otel_annotation_dropped_total` | `reason` | Annotations that did not reach Grafana since process start, by bounded `reason`. `duplicate` is the STEADY STATE on a snapshot-shaped source (a key stays inside its expiry window for days and is re-observed every poll) and is not a fault; `queue_full` and `local_rate_limited` are this process's own guards; `unauthorized`, `rate_limited`, `rejected`, `server_error` and `transport` are Grafana's verdict. A climbing `unauthorized` is an expired or under-scoped token. |
| `tailscale2otel.annotation.published` | `{annotation}` | counter | `tailscale2otel_annotation_published_total` | `category` | Annotations accepted by the Grafana annotations API since process start, by `category`. Only emitted when `grafana_annotations.url` is set; a deployment with annotations off has no such series at all. |
| `tailscale2otel.api.availability` | `1` | gauge | `tailscale2otel_api_availability_ratio` | `tailscale_collector`, `tailscale_api_operation`, `tailscale_api_state` | `1` for the API operation's current availability state, `0` for every other state. The full state set is always emitted (zero-seeded), so a state that stops occurring reads as `0` rather than disappearing. States: `supported`, `disabled` (feature off or not configured — expected, not a fault), `scope_denied` (HTTP 403, the credential lacks the scope), `credential_rejected` (HTTP 401), `transient_failure` (429/5xx/network/timeout — retryable), `request_rejected` (any other 4xx: the API refused the request this exporter built, so retrying it unchanged cannot succeed — terminal and our fault), `unknown` (not yet probed). **`disabled` and `scope_denied` are deliberately distinct** — alert on the latter, never the former. **`request_rejected` and `transient_failure` are likewise distinct**: conflating them let a 400 on every single tick masquerade as upstream flakiness (#523). |
| `tailscale2otel.api.duration` | `s` | histogram | `tailscale2otel_api_duration_seconds` | `endpoint`, `http_response_status_code` | Tailscale API request wall-clock latency in seconds, by endpoint and HTTP status code. Covers the full logical request including any retry backoff (not just server time). Use the 429 status-code bucket here plus tailscale2otel.api.retries for rate-limit visibility — the Tailscale API exposes no rate-limit-remaining headers. When tracing is enabled, datapoints carry trace exemplars linking to the API request span. |
| `tailscale2otel.api.last_probe` | `s` | gauge | `tailscale2otel_api_last_probe_seconds` | `tailscale_collector`, `tailscale_api_operation` | Unix timestamp the API operation was last probed (dashboards subtract `time()`). |
| `tailscale2otel.api.rate_limit.utilization` | `1` | gauge | `tailscale2otel_api_rate_limit_utilization_ratio` | — | `1` when any attempt in the latest logical Tailscale API request encountered provider-side HTTP 429 rate limiting, else `0`, scoped by the runtime's tailnet resource (a **flag**, despite the `_ratio` Prometheus suffix). |
| `tailscale2otel.api.rate_limit.wait` | `s` | histogram | `tailscale2otel_api_rate_limit_wait_seconds` | `endpoint` | Time in seconds a Tailscale API request spent blocked on the client-side rate limiter (`tailscale.http.rate_limit`) before its first attempt, by endpoint. Recorded separately from and excluded from tailscale2otel.api.duration so latency reflects genuine API/network + backoff time. A rising distribution here means the configured rate limit is throttling the poller — raise `rate_limit` or lengthen collector intervals. Only requests that actually waited are recorded (a 0-wait request is skipped). |
| `tailscale2otel.api.requests` | `1` | counter | `tailscale2otel_api_requests_total` | `endpoint`, `http_response_status_code` | Tailscale API requests, by endpoint and HTTP status code. |
| `tailscale2otel.api.retries` | `1` | counter | `tailscale2otel_api_retries_total` | `endpoint` | API retry attempts, by endpoint. |
| `tailscale2otel.build_info` | `1` | gauge | `tailscale2otel_build_info_ratio` | `version`, `go_version` | Constant `1` build-info gauge carrying the build version as the `version` label and the Go runtime version as `go.version`. This is the metrics-side home of the service version: it is kept off the resource (and so off every series as `service_version`) — join it with `group_left` to attribute other metrics to a build. |
| `tailscale2otel.capability.scope_satisfied` | `1` | gauge | `tailscale2otel_capability_scope_satisfied_ratio` | `tailscale_capability` | `1` when the OAuth scopes requested in configuration cover the capability's documented requirement, `0` when they demonstrably do not (a **flag**, despite the `_ratio` suffix). **Advisory startup preflight, not a server answer**: it compares `tailscale.auth.oauth.scopes` against a static map of upstream's documented scopes, so it never blocks collection and a `1` is not a guarantee. Emitted only for capabilities whose scope is modeled AND when the credential is an OAuth client — an API key carries no scope list, and a `0` there would read as a real permission gap. The authoritative signal is `tailscale2otel.capability.status` / `tailscale2otel.api.availability` reaching `scope_denied`. |
| `tailscale2otel.capability.status` | `1` | gauge | `tailscale2otel_capability_status_ratio` | `tailscale_collector`, `tailscale_api_state` | `1` for the enabled collector's current capability state, `0` for every other state (a **flag**, despite the `_ratio` Prometheus suffix). The full state set is zero-seeded, so a collector that recovers falls to `0` on its old state instead of pinning there forever. The value is the most operator-relevant state across all of that collector's probed operations, so a partial `scope_denied` is never masked by a sibling success. Use it to render an optional-feature panel's empty state: `disabled` means the tailnet does not have the feature, `scope_denied` means the credential cannot see it. |
| `tailscale2otel.checkpoint.disk.size` | `By` | gauge | `tailscale2otel_checkpoint_disk_size_bytes` | — | On-disk size of the checkpoint file in bytes. |
| `tailscale2otel.checkpoint.persist.age` | `s` | gauge | `tailscale2otel_checkpoint_persist_age_seconds` | — | Seconds since the checkpoint file was last successfully written (file mtime). |
| `tailscale2otel.checkpoint.persist.errors` | `1` | counter | `tailscale2otel_checkpoint_persist_errors_total` | `tailscale_collector` | Count of checkpoint-persistence failures, by collector (the window succeeded but its high-water mark could not be saved). |
| `tailscale2otel.component.errors` | `1` | counter | `tailscale2otel_component_errors_total` | `component` | Failures of non-collector subsystems (receivers, admin server, streaming auto-configure), by component. |
| `tailscale2otel.config.valid` | `1` | gauge | `tailscale2otel_config_valid_ratio` | — | `1` when the running configuration passes Validate(), else `0` (a **flag**, despite the `_ratio` suffix). Normally `1` at runtime since invalid config fails startup; exposed as an alertable invariant. |
| `tailscale2otel.config.warnings` | `1` | gauge | `tailscale2otel_config_warnings_ratio` | — | Number of active configuration advisories from config.Warnings() (a **count**, despite the `_ratio` suffix). Non-zero means startup logged WARN-level advisories worth reviewing. |
| `tailscale2otel.coordination.handovers` | `{handover}` | counter | `tailscale2otel_coordination_handovers_total` | `coordination_mode`, `coordination_lease_name`, `coordination_namespace`, `coordination_identity` | Completed Kubernetes Lease leadership handovers into this process, derived from the same process-lifetime Lease observation used for self-fencing. A handover is counted exactly once when the observed holder changes from a different non-empty identity to this process. The initial observation emits zero, so a process restart does not look like flapping; a deleted Lease or fencing without an incoming holder is not a completed handover. |
| `tailscale2otel.coordination.leader` | `1` | gauge | `tailscale2otel_coordination_leader_ratio` | `coordination_mode`, `coordination_lease_name`, `coordination_namespace`, `coordination_identity`, `coordination_state` | Lease leadership flag: `1` while this pod holds the active-passive Kubernetes Lease, otherwise `0`. In Kubernetes coordination mode, every enabled delivery path exposes this process-level metric while a pod is standby; the Prometheus pull path keeps collector series leader-only. A stepped-down value marks a deliberate stop after renewal failure. |
| `tailscale2otel.dedup.evictions` | `1` | counter | `tailscale2otel_dedup_evictions_total` | `dedup_set` | Keys evicted from a de-duplication set because it was at capacity, by set. Steady-state evictions are NORMAL and not a problem: flow dedup keys embed each batch's window timestamps, so keys are effectively unique, and once the fixed-size set first fills it evicts exactly one key per insert forever — even in a perfectly healthy deployment. The real overflow signal is evictions approaching the set's capacity *within a single poll interval* (overlap keys aged out before the next poll can dedup against them, i.e. genuine boundary double-counting), NOT sustained nonzero evictions. |
| `tailscale2otel.dedup.hits` | `1` | counter | `tailscale2otel_dedup_hits_total` | `dedup_set` | Duplicate keys suppressed by a de-duplication set, by set (a hit is a record dropped because its key was already seen — proves the set is actually de-duplicating; a **count**, despite the `_total` suffix). |
| `tailscale2otel.dedup.overlap_horizon` | `s` | gauge | `tailscale2otel_dedup_overlap_horizon_seconds` | `dedup_set` | Configured poll-overlap horizon in seconds for a de-duplication set, by set. The flow value is the larger of its poll interval and explicit replay overlap; audit and webhook-cross values use the audit poll interval. |
| `tailscale2otel.dedup.size` | `1` | gauge | `tailscale2otel_dedup_size_ratio` | `dedup_set` | Keys currently held in a cross-source de-duplication set, by set (a **count**, despite the `_ratio` suffix). |
| `tailscale2otel.dedup.youngest_eviction_age` | `s` | gauge | `tailscale2otel_dedup_youngest_eviction_age_seconds` | `dedup_set` | Smallest residency age in seconds observed at capacity eviction since the previous self-observability reporting interval, by de-duplication set. Absent during intervals with no capacity eviction; values below the poll overlap horizon indicate the set may be undersized. |
| `tailscale2otel.enrich.cache_age` | `s` | gauge | `tailscale2otel_enrich_cache_age_seconds` | — | Age of the device-enrichment cache (time since its last successful refresh). Emitted at export time so it grows while stale; alert on it to detect a devices collector that has stopped refreshing. |
| `tailscale2otel.enrich.cache_size` | `1` | gauge | `tailscale2otel_enrich_cache_size_ratio` | — | Number of devices in the enrichment cache (a **count**, despite `_ratio`). |
| `tailscale2otel.export.datapoints` | `{datapoint}` | counter | `tailscale2otel_export_datapoints_total` | — | Metric data points handed to the OTLP metric exporter (the DPM cost proxy). Counts every point across all instruments per export cycle; includes this self-metric (+1/cycle). |
| `tailscale2otel.export.diagnostics.suppressed` | `1` | counter | `tailscale2otel_export_diagnostics_suppressed_total` | `signal`, `error_type` | Export-failure diagnostic log lines suppressed during a sustained OTLP outage, by signal and error class. Exact — never itself rate-limited. |
| `tailscale2otel.export.duration` | `s` | histogram | `tailscale2otel_export_duration_seconds` | `signal`, `outcome` | Wall-clock duration of each OTLP `Export()` call to the backend, by signal and outcome. `signal`=metrics\|logs, `outcome`=success\|failure. One observation per export cycle per signal; use it for export-latency p50/p99 and to tell a slow backend from a failing one. |
| `tailscale2otel.export.failures` | `1` | counter | `tailscale2otel_export_failures_total` | `error_type`, `signal` | OTLP export failures, by error class. |
| `tailscale2otel.export.log_records` | `{record}` | counter | `tailscale2otel_export_log_records_total` | — | Log records handed to the OTLP log exporter (the log-volume cost driver; flow/audit logs dominate). Counts every record per export batch. |
| `tailscale2otel.export.spans` | `{span}` | counter | `tailscale2otel_export_spans_total` | — | Spans handed to the OTLP trace exporter (the trace cost proxy). Counts every span per export batch. |
| `tailscale2otel.flow_store.journal.size` | `By` | gauge | `tailscale2otel_flow_store_journal_size_bytes` | — | Current SQLite write-ahead journal sidecar size in bytes for the persistent flow store, scoped by the runtime's tailnet resource. Absent for the in-memory backend. |
| `tailscale2otel.flow_store.last_checkpoint_timestamp` | `s` | gauge | `tailscale2otel_flow_store_last_checkpoint_timestamp_seconds` | — | Unix timestamp in seconds of the most recent successful SQLite WAL checkpoint for the persistent flow store, scoped by the runtime's tailnet resource. Absent until the first checkpoint and for the in-memory backend. |
| `tailscale2otel.ingest.capture.delay` | `s` | histogram | `tailscale2otel_ingest_capture_delay_seconds` | `source`, `signal` | Delay in seconds between an event and its upstream capture/observation timestamp, when the wire format supplies both. Inverted timestamps are counted by ingest.timestamp_skew and clamped to zero. |
| `tailscale2otel.ingest.event.age` | `s` | histogram | `tailscale2otel_ingest_event_age_seconds` | `source`, `signal` | Age in seconds of each accepted event at ingestion time, by bounded source and signal. Future event timestamps are counted by ingest.timestamp_skew and clamped to zero. |
| `tailscale2otel.ingest.last_event_timestamp` | `s` | gauge | `tailscale2otel_ingest_last_event_timestamp_seconds` | `source`, `signal` | Greatest accepted event timestamp as Unix seconds, by bounded source and signal. Delayed retries and backfills do not move this gauge backwards. |
| `tailscale2otel.ingest.records` | `{record}` | counter | `tailscale2otel_ingest_records_total` | `source`, `signal` | Records accepted per ingestion path and signal type. `source`=poll\|stream\|webhook\|objectstore, `signal`=flow\|audit\|webhook. The unified cross-path ingestion-volume view (the per-path receivers also expose domain counters). |
| `tailscale2otel.ingest.size` | `By` | counter | `tailscale2otel_ingest_size_bytes_total` | `source` | Decompressed/decoded payload bytes received per ingestion path, by `source`=stream\|webhook\|objectstore; the poll path has no wire body to measure and emits none. For objectstore this is the decompressed object size (matching stream/webhook's meaning), NOT the compressed bytes actually read from the bucket — see tailscale2otel.objectstore.bytes for that. Note: ingress bytes do not directly drive Grafana Cloud cost — see export.datapoints/export.log_records for that. |
| `tailscale2otel.ingest.timestamp_skew` | `{event}` | counter | `tailscale2otel_ingest_timestamp_skew_total` | `source`, `signal` | Accepted events with an inverted timestamp relationship (event after local acceptance, or capture before event), by bounded source and signal. |
| `tailscale2otel.ingress_wal.completion.markers` | `1` | gauge | `tailscale2otel_ingress_wal_completion_markers_ratio` | — | Transient ingress-WAL completion markers awaiting durable cleanup (a **count**, despite the `_ratio` Prometheus suffix). |
| `tailscale2otel.ingress_wal.orphan.size` | `By` | gauge | `tailscale2otel_ingress_wal_orphan_size_bytes` | — | Encoded on-disk bytes consumed by retained ingress-WAL staging files. |
| `tailscale2otel.ingress_wal.orphan.stages` | `1` | gauge | `tailscale2otel_ingress_wal_orphan_stages_ratio` | — | Ingress-WAL staging files retained for bounded recovery cleanup (a **count**, despite the `_ratio` Prometheus suffix). |
| `tailscale2otel.ingress_wal.pending.entries` | `1` | gauge | `tailscale2otel_ingress_wal_pending_entries_ratio` | — | Durable ingress-WAL entries awaiting successful processor application and metric/log flush (a **count**, despite the `_ratio` Prometheus suffix). |
| `tailscale2otel.ingress_wal.pending.entries.fill` | `1` | gauge | `tailscale2otel_ingress_wal_pending_entries_fill_ratio` | — | Fraction of the configured ingress-WAL entry capacity occupied by durable entries (0..1). |
| `tailscale2otel.ingress_wal.pending.size` | `By` | gauge | `tailscale2otel_ingress_wal_pending_size_bytes` | — | Encoded on-disk bytes consumed by durable ingress-WAL entries. |
| `tailscale2otel.ingress_wal.pending.size.fill` | `1` | gauge | `tailscale2otel_ingress_wal_pending_size_fill_ratio` | — | Fraction of the configured ingress-WAL byte capacity occupied by durable entries (0..1). |
| `tailscale2otel.log.record.truncated` | `1` | counter | `tailscale2otel_log_record_truncated_total` | `field` | Count of log records whose body or an attribute value was truncated to a bounded length before export, by field. |
| `tailscale2otel.log.truncated.bytes` | `By` | counter | `tailscale2otel_log_truncated_bytes_total` | `field` | Bytes dropped from log record bodies/attribute values by truncation, by field. |
| `tailscale2otel.metrics.auth.rejected` | `1` | counter | `tailscale2otel_metrics_auth_rejected_total` | `reason` | Prometheus `/metrics` requests refused by the auth gate, by `reason`: `missing_credentials`, `bad_credentials`, or `auth_required` (no `prometheus.auth.token` on a network-reachable bind — misconfiguration rather than a bad caller). Counted per request; the `auth_required` misconfiguration is logged once per process rather than once per scrape, so this counter is the only per-request signal. |
| `tailscale2otel.metrics.scrape.duration` | `s` | histogram | `tailscale2otel_metrics_scrape_duration_seconds` | `outcome` | Wall-clock seconds spent serving one Prometheus `/metrics` request, by the same classified `outcome` as tailscale2otel.metrics.scrape.requests. Covers gather plus encode plus write, so it is what the scraper's own timeout is racing against. |
| `tailscale2otel.metrics.scrape.gather_errors` | `1` | counter | `tailscale2otel_metrics_scrape_gather_errors_total` | — | Prometheus registry Gather errors hit while serving `/metrics`. The handler runs with `ContinueOnError` (#103), so these return HTTP 200 with the *remaining* series and are otherwise invisible — a non-zero rate means some series are being dropped from every scrape, most commonly a duplicate-series collision after `pii_filter.tailnet_name` removed the distinguishing label. |
| `tailscale2otel.metrics.scrape.in_flight` | `{request}` | updowncounter | `tailscale2otel_metrics_scrape_in_flight` | — | Prometheus `/metrics` requests currently being served. A value pinned at `prometheus.max_requests_in_flight` means scrapes are being shed; a value that climbs and never returns to zero means collection is hanging rather than slow. |
| `tailscale2otel.metrics.scrape.requests` | `{request}` | counter | `tailscale2otel_metrics_scrape_requests_total` | `outcome` | Prometheus `/metrics` scrapes served after passing the auth gate, by classified `outcome`: `success` (2xx), `unavailable` (503 — the request was shed by `prometheus.max_requests_in_flight` or exceeded `prometheus.timeout`), or `error` (any other status). Rejected requests are NOT counted here; they are counted by tailscale2otel.metrics.auth.rejected. A flat `success` rate is how you tell a scraper that stopped asking apart from an endpoint that stopped answering. |
| `tailscale2otel.pii_filter.category` | `1` | gauge | `tailscale2otel_pii_filter_category_ratio` | `category` | PII redaction state per category: `1` = emitted, `0` = redacted (a **flag**, despite the `_ratio` Prometheus suffix). One datapoint per category, emitted each interval so dashboards can conditionally render PII-bearing panels. |
| `tailscale2otel.processor.dropped` | `1` | counter | `tailscale2otel_processor_dropped_total` | `signal`, `reason` | Records/spans dropped because this app's own log/trace batch processor queue was full when offered, by signal and reason. |
| `tailscale2otel.processor.queue.capacity` | `1` | gauge | `tailscale2otel_processor_queue_capacity_ratio` | `signal` | Configured maximum size of this app's own log/trace batch processor queue, by signal. |
| `tailscale2otel.processor.queue.size` | `1` | gauge | `tailscale2otel_processor_queue_size_ratio` | `signal` | Current number of records/spans buffered in this app's own log/trace batch processor queue, awaiting export, by signal. |
| `tailscale2otel.profiling.upload.attempts` | `1` | counter | `tailscale2otel_profiling_upload_attempts_total` | — | Profile upload attempts to Pyroscope, successful or not (one per profile type per `profiling.pyroscope.upload_rate` period). Emitted only when the Pyroscope push agent is enabled. A flat line here with the agent enabled means the agent is not uploading at all, which is a different fault from uploads being rejected. |
| `tailscale2otel.profiling.upload.consecutive_failures` | `1` | gauge | `tailscale2otel_profiling_upload_consecutive_failures_ratio` | — | Current unbroken profile-upload failure streak, reset to `0` by any success (a **count**, despite the `_ratio` Prometheus suffix). Distinguishes a blip from a sustained outage without needing a rate window. |
| `tailscale2otel.profiling.upload.duration` | `s` | histogram | `tailscale2otel_profiling_upload_duration_seconds` | — | Wall-clock seconds per profile upload attempt, including failed ones. Rising latency here is the early warning for the upload timeout that follows. |
| `tailscale2otel.profiling.upload.failures` | `1` | counter | `tailscale2otel_profiling_upload_failures_total` | `error_type` | Profile upload attempts that failed, by bounded `error.type` (timeout\|canceled\|unauthenticated\|rate_limited\|unavailable\|tls\|invalid\|other). The class set is CLOSED and never contains any part of the server's response — a Pyroscope error body is a credential-echo surface. `unauthenticated` means the basic-auth credential or tenant is wrong; `tls` means the custom CA / client certificate did not satisfy the handshake. |
| `tailscale2otel.profiling.upload.last_success` | `s` | gauge | `tailscale2otel_profiling_upload_last_success_seconds` | — | Unix seconds of the most recent SUCCESSFUL profile upload; `0` until the first one succeeds. Alert on `time() - this` exceeding several upload periods to catch profiles silently stopping — the attempts counter keeps climbing during an outage, so it cannot tell you this. |
| `tailscale2otel.receiver.misconfigured` | `1` | gauge | `tailscale2otel_receiver_misconfigured_ratio` | `receiver` | `1` when an enabled ingestion receiver is bound to a network-reachable address without its required token or signing secret and therefore refuses every request with HTTP 403, else `0`, by receiver (`streaming` or `webhook`). |
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
| `tailscale2otel.scrape.duration.histogram` | `s` | histogram | `tailscale2otel_scrape_duration_histogram_seconds` | `tailscale_collector` | Distribution of scrape wall-clock durations, per collector; additional to `scrape.duration`, recorded with the scrape span so exemplars link a bucket back to the trace. |
| `tailscale2otel.scrape.errors` | `1` | counter | `tailscale2otel_scrape_errors_total` | `tailscale_collector`, `error_type` | Count of scrape errors, by collector and error class. |
| `tailscale2otel.scrape.last_timestamp` | `s` | gauge | `tailscale2otel_scrape_last_timestamp_seconds` | `tailscale_collector` | Unix timestamp the last scrape *finished* (success **or** failure); pair with `scrape.success` to detect last-success staleness. |
| `tailscale2otel.scrape.staleness` | `s` | gauge | `tailscale2otel_scrape_staleness_seconds` | `tailscale_collector` | Seconds since this collector's last successful scrape (counts up from process start until the first success); pair with `scrape.success` for freshness alerting. |
| `tailscale2otel.scrape.success` | `1` | gauge | `tailscale2otel_scrape_success_ratio` | `tailscale_collector` | `1` if the last scrape for that collector succeeded, else `0`. |
| `tailscale2otel.series.active` | `{series}` | gauge | `tailscale2otel_series_active` | `metric_name` | Exact distinct active time series emitted for `metric.name` during the last export interval; bounded by a per-metric cap (the value pins at the cap when exceeded). A **count**. |
| `tailscale2otel.series.by_group` | `{series}` | gauge | `tailscale2otel_series_by_group` | `metric_group` | Active time series emitted during the last export interval, summed by the catalog group that owns each metric (a roll-up of tailscale2otel.series.active by `metric.group`). Uncataloged metric names (e.g. node-metrics passthrough) bucket under `other`. A **count**. |
| `tailscale2otel.series.limit` | `{series}` | gauge | `tailscale2otel_series_limit` | — | Effective per-metric active-series cap (`cardinality.metric_limit`): the point at which excess series collapse into `otel_metric_overflow` (silent per-series loss). Emitted only when a positive limit is configured. A **count**. |
| `tailscale2otel.series.overflowing` | `1` | gauge | `tailscale2otel_series_overflowing_ratio` | `metric_name` | 1 when `metric.name` reached the per-metric series cap during the last interval (excess series silently dropped into `otel_metric_overflow`), else 0. Always 0 when no positive `cardinality.metric_limit` is configured. |
| `tailscale2otel.subrequest.attempts` | `{request}` | counter | `tailscale2otel_subrequest_attempts_total` | `tailscale_collector`, `tailscale_subrequest` | Per-entity subrequests attempted, by bounded subrequest type (e.g. one posture-attributes call per device). |
| `tailscale2otel.subrequest.coverage` | `1` | gauge | `tailscale2otel_subrequest_coverage_ratio` | `tailscale_collector`, `tailscale_subrequest` | Fraction of per-entity subrequests that succeeded on the last pass, in `[0,1]` (a genuine ratio, unlike most `_ratio` metrics here). `1` when nothing was attempted — an empty tailnet has complete coverage of the entities it has. |
| `tailscale2otel.subrequest.failures` | `{request}` | counter | `tailscale2otel_subrequest_failures_total` | `tailscale_collector`, `tailscale_subrequest`, `tailscale_api_state` | Per-entity subrequests that failed, by bounded subrequest type and availability state. A single entity's failure is non-fatal to the enclosing snapshot, so without this signal a missing scope silently degrades coverage while the collector still reports a clean scrape. |
| `tailscale2otel.tls.cert.not_after` | `s` | gauge | `tailscale2otel_tls_cert_not_after_seconds` | `component` | Unix seconds of the active TLS certificate's notAfter (expiry), by listener component (admin\|metrics\|stream). Alert on this approaching the current time to catch an expiring certificate before clients start failing handshakes. |
| `tailscale2otel.tls.cert.not_before` | `s` | gauge | `tailscale2otel_tls_cert_not_before_seconds` | `component` | Unix seconds of the active TLS certificate's notBefore, by listener component (admin\|metrics\|stream). Emitted only for a listener with TLS configured; updates on every successful certificate reload. |
| `tailscale2otel.tls.cert.reload.failures` | `1` | counter | `tailscale2otel_tls_cert_reload_failures_total` | `component` | TLS certificate reload attempts that failed to produce a valid keypair, by listener component (admin\|metrics\|stream). The listener keeps serving the last known-good certificate; a non-zero rate here is the signal that a rotation went wrong. |
| `tailscale2otel.tls.cert.reload.last_success` | `s` | gauge | `tailscale2otel_tls_cert_reload_last_success_seconds` | `component` | Unix seconds of the most recent successful TLS certificate reload, by listener component (admin\|metrics\|stream). A rotated file on disk is picked up on the next handshake at least this recently. |
| `tailscale2otel.up` | `1` | gauge | `tailscale2otel_up_ratio` | — | Liveness flag: `1` while the service is running and reporting. |
| `tailscale2otel.update_available` | `1` | gauge | `tailscale2otel_update_available_ratio` | — | `1` when a newer tailscale2otel release is available on GitHub than the running build, else `0` (a **flag**, despite the `_ratio` Prometheus suffix). Emitted only when `version_checks.self` is enabled and both the running and latest versions parse — dev builds (version `dev`) never emit. Fail-open: a blocked/failed GitHub fetch emits nothing. |
<!-- END GENERATED -->

### Network / flow (`tailscale.network.*`, `tailscale.config.audit.*`)

Aggregated, low-cardinality counters derived from flow logs and audit logs. The full-fidelity
per-connection detail is emitted as **log records** (see [Log events](#log-events)).

> **Exit traffic carries no destination — and often no source.** Records with `traffic_type=exit`
> report byte and packet counts against the *reporting* node only. A live capture found no `dst` on
> any exit entry, and no `src` on roughly half of them; none carried a protocol number either (so
> `network.transport` is `unknown`). Attributes derived from an absent endpoint are **omitted**
> rather than filled with `unknown` — a missing `tailscale.dst.node` means the data never had one,
> not that a lookup failed. To measure exit traffic use **`tailscale.exit_node.io`** and
> **`tailscale.exit_node.packets`**, which attribute by relaying node: the only dimension exit
> records actually supply. The same omission applies to any traffic type with an absent endpoint;
> `virtual`, `subnet` and `physical` records carry both endpoints in practice.

> **A relayed connection has no destination node.** When `tailscale_path` is `derp`, Tailscale
> reported the loopback marker `127.3.3.40` in place of an endpoint address and the DERP **region
> ID** in place of the port. That is infrastructure, not a peer, so `tailscale_dst_node` and
> `tailscale_dst_service` are **omitted** on those series rather than naming a device that was never
> a destination (or an IANA service that was never contacted). The relay is described by
> `tailscale_path=derp` and `tailscale_derp_region_id`, which is what it actually is. **The
> counterparty is not lost:** on `physical` traffic `src` is the peer's overlay address, so the node
> you want is `tailscale_src_node`, unaffected. For the same reason a relayed connection is not
> counted in `tailscale.network.unique.dst_peers`/`dst_ports` — a marker is not a distinct peer, and
> a region ID is not a port. **Totals are unchanged**: only labels are dropped, never data points.
> The flow **log** keeps the raw `destination_address`/`destination_port` (it is the full-fidelity
> record of what the wire said) and omits only `tailscale_dst_node`; the `/flows` page does the same,
> showing a dash where the device name would be.

<!-- BEGIN GENERATED: metrics groups="Network / flow" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.config.audit.changes` | `{event}` | counter | `tailscale_config_audit_changes_total` | `tailscale_audit_change`, `tailscale_audit_action`, `tailscale_actor_type` | Curated security- and lifecycle-relevant configuration-audit changes, by change category, action, and actor type. |
| `tailscale.config.audit.deferred.delay` | `s` | histogram | `tailscale_config_audit_deferred_delay_seconds` | — | Distribution of time Tailscale deferred configuration-audit records before logging them, in seconds. Emitted only when both eventTime and deferredAt are present and ordered. |
| `tailscale.config.audit.events` | `{event}` | counter | `tailscale_config_audit_events_total` | `tailscale_audit_action`, `tailscale_audit_origin` | Configuration-audit events, by action and origin. |
| `tailscale.config.audit.processing.delay` | `s` | histogram | `tailscale_config_audit_processing_delay_seconds` | — | Distribution of time from a configuration-audit record's deferredAt (or eventTime when not deferred) to local processor acceptance, in seconds. Emitted only for a present, non-future source timestamp. |
| `tailscale.config.audit.schema_drift` | `{observation}` | counter | `tailscale_config_audit_schema_drift_total` | `field`, `status` | Configuration-audit schema vocabulary observations, by enum field and whether its value is known to this collector version. |
| `tailscale.exit_node.io` | `By` | counter | `tailscale_exit_node_io_bytes_total` | `tailscale_exit_node`, `network_io_direction` | Bytes relayed through each exit node, by direction. Attributed to the reporting node of `traffic_type=exit` flow records (`tailscale.exit_node` = its hostname, or nodeId on a cache miss). Bounded by exit-node count. **Gated** by `cardinality.flow.exit_node_attribution` (default on); independent of the rollup/raw metric mode. |
| `tailscale.exit_node.packets` | `{packet}` | counter | `tailscale_exit_node_packets_total` | `tailscale_exit_node`, `network_io_direction` | Packets relayed through each exit node, with the same dimensions as tailscale.exit_node.io. |
| `tailscale.network.data_quality` | `{violation}` | counter | `tailscale_network_data_quality_total` | `source`, `reason` | Semantically invalid flow records rejected before processor side effects, classified by a closed ingestion source and validation reason. |
| `tailscale.network.dedup.conflicts` | `{conflict}` | counter | `tailscale_network_dedup_conflicts_total` | `scope`, `tailscale_traffic_type` | Duplicate flow connections whose counters conflict with the first observation; first observed counters remain authoritative. |
| `tailscale.network.field.observations` | `{observation}` | counter | `tailscale_network_field_observations_total` | `tailscale_traffic_type`, `field_class`, `state` | Observed flow connection field completeness by traffic type, field class, and present or missing state. Missing fields are source evidence, not an inferred Destination Logging configuration setting. |
| `tailscale.network.flow.logs_dropped` | `{record}` | counter | `tailscale_network_flow_logs_dropped_total` | — | Flow LOG records suppressed by the per-window volume guard (collectors.flowlogs.max_log_records_per_window); 0 unless truncating. Metrics are never dropped, only logs. |
| `tailscale.network.flows` | `{flow}` | counter | `tailscale_network_flows_total` | `network_transport`, `tailscale_traffic_type` | Count of distinct flows observed (lower cardinality than network.io/packets). |
| `tailscale.network.io` | `By` | counter | `tailscale_network_io_bytes_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `source_port`, `destination_port`, `tailscale_dst_service`, `tailscale_path`, `tailscale_derp_region_id`, `tailscale_src_user`, `tailscale_src_tags`, `tailscale_src_os`, `tailscale_dst_user`, `tailscale_dst_tags`, `tailscale_dst_os`, `source_geo_country_iso_code`, `source_geo_continent_code`, `destination_geo_country_iso_code`, `destination_geo_continent_code` | Bytes transferred on the tailnet, by direction, transport, traffic type, and source/destination node. Emitted when `cardinality.flow.metrics_mode` is `all` or `both` — under the default `rollup` the bounded network.io.rollup family is emitted instead, and the `cardinality.flow.source_port`/`destination_port`/`identity_dims` toggles have no effect at all. `tailscale.path` (`direct`/`derp`) and, on a relayed connection, the numeric `tailscale.derp.region_id` are carried on **physical** traffic only — the overlay traffic types describe what the tailnet carried, not how, so they carry no path at all rather than one that reads as `direct`. `tailscale.derp.region_id` is NOT joinable with `tailscale.derp.region` on the device latency metrics: that one is a region NAME, this is the numeric ID the flow record supplies, and the API exposes no DERP map to translate between them. The endpoint identity attributes (`tailscale.{src,dst}.{user,tags,os}`) are **gated** by `cardinality.flow.identity_dims` (default off) and additionally require `cardinality.flow.node_dims`, since identity is node-derived. |
| `tailscale.network.io.rollup` | `By` | counter | `tailscale_network_io_rollup_bytes_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `tailscale_dst_service`, `tailscale_path`, `tailscale_derp_region_id`, `tailscale_src_user`, `tailscale_src_tags`, `tailscale_src_os`, `tailscale_dst_user`, `tailscale_dst_tags`, `tailscale_dst_os`, `source_geo_country_iso_code`, `source_geo_continent_code`, `destination_geo_country_iso_code`, `destination_geo_continent_code` | Bytes transferred on the tailnet, bounded top-N rollup: the busiest source/destination node pairs by total bytes are kept per flush and the remainder is folded into a tailscale.src.node/tailscale.dst.node="__other__" series per transport, traffic type, and destination service, so totals are preserved. Carries no L4 ports. Emitted when cardinality.flow.metrics_mode is rollup or both (the default). The `__other__` fold drops the endpoint identity with the node dimensions it derives from: the remainder is many nodes and so has no single user to report. `tailscale.path` (`direct`/`derp`) and, on a relayed connection, the numeric `tailscale.derp.region_id` are carried on **physical** traffic only — the overlay traffic types describe what the tailnet carried, not how, so they carry no path at all rather than one that reads as `direct`. `tailscale.derp.region_id` is NOT joinable with `tailscale.derp.region` on the device latency metrics: that one is a region NAME, this is the numeric ID the flow record supplies, and the API exposes no DERP map to translate between them. The endpoint identity attributes (`tailscale.{src,dst}.{user,tags,os}`) are **gated** by `cardinality.flow.identity_dims` (default off) and additionally require `cardinality.flow.node_dims`, since identity is node-derived. |
| `tailscale.network.packets` | `{packet}` | counter | `tailscale_network_packets_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `source_port`, `destination_port`, `tailscale_dst_service`, `tailscale_path`, `tailscale_derp_region_id`, `tailscale_src_user`, `tailscale_src_tags`, `tailscale_src_os`, `tailscale_dst_user`, `tailscale_dst_tags`, `tailscale_dst_os`, `source_geo_country_iso_code`, `source_geo_continent_code`, `destination_geo_country_iso_code`, `destination_geo_continent_code` | Packets transferred on the tailnet, with the same dimensions as network.io. |
| `tailscale.network.packets.rollup` | `{packet}` | counter | `tailscale_network_packets_rollup_total` | `network_io_direction`, `network_transport`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `tailscale_dst_service`, `tailscale_path`, `tailscale_derp_region_id`, `tailscale_src_user`, `tailscale_src_tags`, `tailscale_src_os`, `tailscale_dst_user`, `tailscale_dst_tags`, `tailscale_dst_os`, `source_geo_country_iso_code`, `source_geo_continent_code`, `destination_geo_country_iso_code`, `destination_geo_continent_code` | Packets transferred on the tailnet, with the same bounded top-N rollup dimensions as network.io.rollup. |
| `tailscale.network.reporter.observations` | `{observation}` | counter | `tailscale_network_reporter_observations_total` | `trust`, `consistency` | Flow-record reporter observations, classified by the configured trust policy and whether the verified reporter node ID agrees with the unverified embedded source reference. Carries no node ID. |
| `tailscale.network.store.dropped` | `{record}` | counter | `tailscale_network_store_dropped_total` | `reason` | Flow observations rejected from the local flow view (the in-memory ring, or the persistent store when flows.store.directory is set) because their timestamps are outside its retention or future-skew bounds. OTLP emission is unaffected. |
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

> **Per-entity gauges drop out on churn (no ghost series).** Metrics are exported with **cumulative**
> temporality (what Grafana Cloud / Mimir ingest). A *synchronous* cumulative gauge would re-export a
> stale value forever once its attribute set has been seen (upstream
> [otel-go #3006](https://github.com/open-telemetry/opentelemetry-go/issues/3006)), so every churning
> per-entity gauge — `tailscale.device.online` and its per-device siblings, the by-version/by-tag/
> by-region/by-CIDR rollups, `tailscale.node.up`, and the per-resolver/per-search-path `tailscale.dns.*`
> — is instead emitted as an **observable** gauge from a per-tick snapshot. An observable gauge under
> cumulative temporality reports only the series observed in the current collection, so when a device is
> **removed or renamed** (or a version/tag/resolver stops appearing) its series simply **drops out of
> the export on the next scrape** rather than ghosting, and it stops consuming a cardinality-limit slot
> (issue #55). Dashboards and alerts on `device.online==1` / `node.up` therefore reflect the live fleet
> without needing to join against a separate recency signal.
>
> One deliberate exception: the **forwarded node-metrics passthrough samples** (the raw series scraped
> from each node's tailscaled `:5252` endpoint) are still synchronous — their names are dynamic and
> include monotonic counters, so snapshot semantics don't apply. If a node leaves discovery, its
> `tailscale.node.up` drops out immediately, but its forwarded gauge samples can linger until an
> exporter restart; rate-based counter panels are unaffected. Size `cardinality.metric_limit` for your
> node churn accordingly.

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
| `tailscale.device.attribute.expiry` | `s` | gauge | `tailscale_device_attribute_expiry_seconds` | `host_name`, `host_id`, `attribute` | Unix epoch seconds of a device posture attribute's expiry; only attributes explicitly set with an expiry (e.g. a `custom:` namespace attribute set via the API with an expiry) appear — most posture attributes never carry one. One series per device per expiring attribute, the namespaced posture key carried as the `attribute` label (same identity as `tailscale.device.attribute{,.info}`). **Gated** by `collect_posture` and the `attribute_namespaces` allow-list. |
| `tailscale.device.attribute.info` | `1` | gauge | `tailscale_device_attribute_info_ratio` | `host_name`, `host_id`, `attribute`, `value` | String/enum device posture attribute info gauge (constant `1`); the namespaced posture key is the `attribute` label and its string value the `value` label (e.g. `intune:complianceState`=`compliant`, `ip:country`=`GB`). **Gated** by `collect_posture` and the `attribute_namespaces` allow-list. |
| `tailscale.device.attributes.dropped` | `1` | gauge | `tailscale_device_attributes_dropped_ratio` | — | Number of distinct posture attribute keys suppressed by `attribute_key_limit` on this collection tick. Emitted as zero when no keys are suppressed; `attribute_value_limit` folds excess string values into `__other__` instead. |
| `tailscale.device.blocks_incoming_connections` | `1` | gauge | `tailscale_device_blocks_incoming_connections_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if the device blocks incoming connections (`blocksIncomingConnections`). **Gated** by `cardinality.per_entity.device`. |
| `tailscale.device.connectivity.direct_capable` | `1` | gauge | `tailscale_device_connectivity_direct_capable_ratio` | `host_name`, `host_id` | `1` if the device looks able to make direct (non-DERP) connections: UDP supported **and** not behind a hard NAT (`clientSupports.udp && !mappingVariesByDestIP`). **Eligibility heuristic, not the live path.** Emitted only when UDP support is reported. Gated by `collect_connectivity` + `cardinality.per_entity.device`. |
| `tailscale.device.connectivity.endpoints` | `1` | gauge | `tailscale_device_connectivity_endpoints_ratio` | `host_name`, `host_id` | Number of magicsock UDP endpoint candidates the device advertises (`clientConnectivity.endpoints` length; a **count**, despite `_ratio`). The endpoint addresses themselves are never emitted. Gated by `collect_connectivity` + `cardinality.per_entity.device`. |
| `tailscale.device.connectivity.hard_nat` | `1` | gauge | `tailscale_device_connectivity_hard_nat_ratio` | `host_name`, `host_id` | `1` if the device is behind a hard/symmetric NAT (`clientConnectivity.mappingVariesByDestIP`), which inhibits direct connections. **Eligibility, not the live path** (live direct-vs-relay needs node-local APIs). Gated by `collect_connectivity` + `cardinality.per_entity.device`. |
| `tailscale.device.connectivity.ipv6` | `1` | gauge | `tailscale_device_connectivity_ipv6_ratio` | `host_name`, `host_id` | `1` if the device OS supports IPv6 (`clientSupports.ipv6`), regardless of IPv6 internet availability. Emitted only when reported. Gated by `collect_connectivity` + `cardinality.per_entity.device`. |
| `tailscale.device.connectivity.udp` | `1` | gauge | `tailscale_device_connectivity_udp_ratio` | `host_name`, `host_id` | `1` if UDP traffic is usable on the device's current network (`clientSupports.udp`); `0` forces DERP relaying. Emitted only when reported. Gated by `collect_connectivity` + `cardinality.per_entity.device`. |
| `tailscale.device.derp.latency` | `s` | gauge | `tailscale_device_derp_latency_seconds` | `host_name`, `host_id`, `tailscale_derp_region`, `tailscale_derp_preferred` | Latency from the device to a DERP region; one series per region. |
| `tailscale.device.distro` | `1` | gauge | `tailscale_device_distro_ratio` | `host_name`, `host_id`, `tailscale_distro_name`, `tailscale_distro_codename` | Per-device distribution info gauge (constant `1`), carrying the device identity plus its normalized `distro.name`/`distro.codename`. Emitted only for devices that report a distribution. **Gated** by `cardinality.per_entity.device`. |
| `tailscale.device.exit_node` | `1` | gauge | `tailscale_device_exit_node_ratio` | `host_name`, `host_id`, `tailscale_exit_node_enabled` | Info gauge (constant `1`) emitted once per device that advertises an exit route; `tailscale.exit_node.enabled` is `true` when the device's default route is approved. Gated by `cardinality.per_entity.device`. |
| `tailscale.device.key.expiry` | `s` | gauge | `tailscale_device_key_expiry_seconds` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | Unix timestamp the device node key expires. |
| `tailscale.device.key_expiry_disabled` | `1` | gauge | `tailscale_device_key_expiry_disabled_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if the device's node key is set never to expire (`keyExpiryDisabled`), else `0`. These are exactly the devices excluded from `tailscale.device.key.expiry` and the `tailscale.devices.key_expiry` histogram, so this gauge is how they stay visible. **Gated** by `cardinality.per_entity.device`, and not emitted at all on a control plane that does not report key-expiry state. |
| `tailscale.device.last_seen` | `s` | gauge | `tailscale_device_last_seen_seconds` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | Unix timestamp the device was last seen. |
| `tailscale.device.multiple_connections` | `1` | gauge | `tailscale_device_multiple_connections_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if more than one client has simultaneously connected using this device's identity (`multipleConnections`) — an anomaly/security signal. **Gated** by `cardinality.per_entity.device`. |
| `tailscale.device.online` | `1` | gauge | `tailscale_device_online_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if the device is currently online, else `0`. |
| `tailscale.device.posture` | `1` | gauge | `tailscale_device_posture_ratio` | `host_name`, `host_id`, `os`, `os_version`, `ts_version`, `auto_update`, `encrypted`, `track` | Per-device posture info gauge (constant `1`); device security posture — OS, Tailscale client version, auto-update, state-encrypted, release track — carried as labels. **Gated** by `collect_posture`. |
| `tailscale.device.posture_identity.disabled` | `1` | gauge | `tailscale_device_posture_identity_disabled_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if the device's posture-identity checks are disabled (`postureIdentity.disabled`). Emitted only when the wire `postureIdentity` object is present on the device (absent → no series; this is independent of `collect_posture`, which controls the separate posture-attribute fetch). **Gated** by `cardinality.per_entity.device`. |
| `tailscale.device.routes.advertised` | `{route}` | gauge | `tailscale_device_routes_advertised` | `host_name`, `host_id` | Number of subnet routes the device advertises. **Gated** by `collect_routes`. |
| `tailscale.device.routes.enabled` | `{route}` | gauge | `tailscale_device_routes_enabled` | `host_name`, `host_id` | Number of advertised routes that are enabled/approved. **Gated** by `collect_routes`. |
| `tailscale.device.ssh_enabled` | `1` | gauge | `tailscale_device_ssh_enabled_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if Tailscale SSH is enabled on the device (`sshEnabled`), else `0` — the device accepts SSH sessions authenticated by the tailnet's ACL policy. **Gated** by `cardinality.per_entity.device`, and not emitted at all on a control plane that does not report Tailscale SSH. |
| `tailscale.device.update_available` | `1` | gauge | `tailscale_device_update_available_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | `1` if a Tailscale client update is available for the device. |
| `tailscale.device.version_skew` | `1` | gauge | `tailscale_device_version_skew_ratio` | `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | Minor releases this device's Tailscale client is behind the latest stable (`latest.minor − device.minor`, same major, clamped ≥0; patch-only drift is 0 — see `tailscale.device.update_available` for that). Per-device, gated by `cardinality.per_entity.device`. Emitted only when `version_checks.devices` is enabled, the upstream latest is known, and the device version parses. |
| `tailscale.device_invites.count` | `1` | gauge | `tailscale_device_invites_count_ratio` | `tailscale_device_invite_accepted`, `tailscale_device_invite_allow_exit_node`, `tailscale_device_invite_multi_use`, `tailscale_device_invite_delivery` | Device-share invites (accepted and pending) (a **count**, despite `_ratio`), bucketed by accepted/pending, the exit-node / multi-use exposure flags, and how the invite was delivered (`tailscale.device_invite.delivery`: `emailed` when an invitee address or a send attempt is recorded, `manual_link` when the invite exists but was never emailed, `unknown` when the control plane reports neither). **Gated** by `collect_device_invites` (one API call per device). |
| `tailscale.device_invites.pending_age` | `s` | histogram | `tailscale_device_invites_pending_age_seconds` | — | Distribution of how long **outstanding** (not yet accepted) device-share invites have existed, in seconds. Accepted invites are excluded, and an invite whose creation time the control plane omits is skipped rather than counted as brand new. Shares the tailnet-wide age buckets (1d, 7d, 30d, 90d, 180d, 1y, 2y). **Gated** by `collect_device_invites`. |
| `tailscale.devices.age` | `s` | histogram | `tailscale_devices_age_seconds` | — | Distribution of device age (time since the device joined the tailnet), in seconds. Devices whose `created` timestamp the control plane omits — external/shared-in devices send an empty string — are **skipped**, never recorded as age 0. Buckets are the shared entity-age ladder: 1d, 7d, 30d, 90d, 180d, 1y, 2y. |
| `tailscale.devices.by_country` | `1` | gauge | `tailscale_devices_by_country_ratio` | `geo_country_iso_code`, `geo_continent_code` | Number of devices located in each country, derived from the first globally-routable magicsock endpoint each device advertises (`clientConnectivity.endpoints`) and the local GeoIP database. A **count**, despite `_ratio`. Requires `enrichment.geoip.enabled` and `collect_connectivity`. This is deliberately a fleet ROLLUP rather than a label on the per-device gauges: country is bounded (~250 values), whereas adding the label to an existing per-device series would change its identity and break queries already written against it. Devices with no globally-routable endpoint, or that the database does not cover, are simply absent — there is no `unknown` bucket. The autonomous system is never emitted as a metric at all. |
| `tailscale.devices.by_distro` | `1` | gauge | `tailscale_devices_by_distro_ratio` | `tailscale_distro_name`, `tailscale_distro_codename` | Device count per operating-system distribution (a **count**, despite `_ratio`); one series per `distro.name`/`distro.codename` pair, both lowercased and trimmed. Devices reporting no distribution at all (most non-Linux clients) are excluded rather than folded into an `unknown` bucket; a distribution that publishes no codename gets `tailscale.distro.codename="unknown"`. Capped at 50 distinct pairs, with the remainder folded into `__other__`/`__other__` so the fleet total is preserved. The raw distro **version** is deliberately not a dimension here — it is carried as `os.version` on the per-device gauges. |
| `tailscale.devices.by_tag` | `1` | gauge | `tailscale_devices_by_tag_ratio` | `tailscale_tag` | Device count per ACL tag (a device with N tags counts in N series). **Gated** by `collect_tag_rollup`; capped by `tag_rollup_limit` with overflow tags folded into `tailscale.tag="__other__"`. |
| `tailscale.devices.by_version` | `1` | gauge | `tailscale_devices_by_version_ratio` | `tailscale_client_version` | Device count per normalized Tailscale client version (`major.minor.patch`; unparseable→`unknown`); one series per version. Devices with no reported version (external) are excluded. |
| `tailscale.devices.client_supports` | `1` | gauge | `tailscale_devices_client_supports_ratio` | `tailscale_connectivity_capability` | Number of devices reporting each direct-connectivity capability as supported (a **count**, despite `_ratio`); one series per capability (`udp`/`ipv6`/`pcp`/`pmp`/`upnp`). `hairPinning` is excluded (no longer tracked by Tailscale). Gated by `collect_connectivity`. |
| `tailscale.devices.count` | `1` | gauge | `tailscale_devices_count_ratio` | `os_type`, `tailscale_authorized`, `tailscale_external` | Fleet device count (a **count**, despite `_ratio`), bucketed by OS/authorized/external. |
| `tailscale.devices.direct_capable` | `1` | gauge | `tailscale_devices_direct_capable_ratio` | — | Number of devices that look direct-capable (`udp && !hard_nat`), counted only among devices reporting UDP support (a **count**, despite `_ratio`). Fleet-wide, no labels. Gated by `collect_connectivity`. |
| `tailscale.devices.ephemeral` | `1` | gauge | `tailscale_devices_ephemeral_ratio` | — | Number of ephemeral devices in the tailnet (a **count**, despite `_ratio`). |
| `tailscale.devices.hard_nat` | `1` | gauge | `tailscale_devices_hard_nat_ratio` | — | Number of devices behind a hard/symmetric NAT (a **count**, despite `_ratio`). Fleet-wide, no labels. Gated by `collect_connectivity`. |
| `tailscale.devices.key_expiry` | `d` | histogram | `tailscale_devices_key_expiry_days` | — | Distribution of days until each device's node key expires (negative = already expired; the `(-inf,0]` bucket). Excludes devices with key expiry disabled. Buckets (days): 0, 7, 30, 90, 180, 365. |
| `tailscale.devices.key_expiry_disabled` | `1` | gauge | `tailscale_devices_key_expiry_disabled_ratio` | — | Number of devices with node-key expiry disabled (`keyExpiryDisabled`; a **count**, despite `_ratio`) — these keys never expire and are therefore invisible to `tailscale.devices.key_expiry`. Fleet-wide, no labels. **Not emitted at all** on a control plane that does not report key-expiry state (e.g. Headscale). |
| `tailscale.devices.outdated` | `1` | gauge | `tailscale_devices_outdated_ratio` | — | Number of devices at least `version_checks.devices.outdated_minor_threshold` minor releases behind the latest Tailscale stable (a **count**, despite `_ratio`). Fleet-wide, no labels. Emitted only when `version_checks.devices` is enabled and the upstream latest is known. |
| `tailscale.devices.posture_compliance.failed` | `1` | gauge | `tailscale_devices_posture_compliance_failed_ratio` | `check` | Number of devices that fail each configured exact-match posture compliance check (a **count**, despite `_ratio`). A missing, composite, or different attribute value fails; only successful posture fetches are evaluated, so an API failure is reported separately as subrequest availability rather than as non-compliance. **Gated** by `collect_posture` and `posture_compliance_checks`. |
| `tailscale.devices.ssh_enabled` | `1` | gauge | `tailscale_devices_ssh_enabled_ratio` | — | Number of devices with Tailscale SSH enabled (`sshEnabled`; a **count**, despite `_ratio`). Fleet-wide, no labels — emitted regardless of `cardinality.per_entity.device`. **Not emitted at all** on a control plane with no Tailscale-SSH concept (e.g. Headscale), so absence never reads as a confident zero. |
| `tailscale.devices.untagged` | `1` | gauge | `tailscale_devices_untagged_ratio` | — | Number of non-external devices with no ACL tags (a **count**, despite `_ratio`); a tagging-hygiene signal. External (shared-in) devices are excluded — they can't be tagged by this tailnet. |
| `tailscale.exit_nodes.count` | `1` | gauge | `tailscale_exit_nodes_count_ratio` | `tailscale_exit_node_state` | Number of exit nodes in the tailnet (a **count**, despite `_ratio`); `tailscale.exit_node.state=advertised` counts devices advertising a default route (`0.0.0.0/0` or `::/0`), `=enabled` counts those whose default route is approved/enabled. |
| `tailscale.fleet.latest_version` | `1` | gauge | `tailscale_fleet_latest_version_ratio` | `tailscale_client_version` | Always `1`; an info gauge whose `tailscale.client_version` label carries the latest Tailscale stable client version (`major.minor.patch`) as fetched from pkgs.tailscale.com. Emitted only when `version_checks.devices` is enabled and the upstream fetch has succeeded. |
| `tailscale.organization.tailnets.count` | `1` | gauge | `tailscale_organization_tailnets_count_ratio` | — | Number of tailnet IDs returned by the configured alpha Organizations API roster (a **count**, despite the `_ratio` Prometheus suffix). Discovery inventories the organization; collector runtimes still require explicit per-tailnet credentials. |
| `tailscale.subnet_routes.advertised` | `{route}` | gauge | `tailscale_subnet_routes_advertised` | — | Number of distinct **subnet** CIDRs advertised by at least one device (exit-node default routes excluded). |
| `tailscale.subnet_routes.enabled` | `{route}` | gauge | `tailscale_subnet_routes_enabled` | — | Number of distinct subnet CIDRs approved/enabled on at least one device (exit-node default routes excluded). |
| `tailscale.subnet_routes.routers` | `1` | gauge | `tailscale_subnet_routes_routers_ratio` | `tailscale_route_cidr` | Number of devices advertising each subnet CIDR — route redundancy (a **count**, despite `_ratio`); one series per CIDR. **Gated** by `cardinality.subnet_route_rollup`. Exit-node default routes excluded. |
| `tailscale.subnet_routes.unapproved` | `{route}` | gauge | `tailscale_subnet_routes_unapproved` | — | Number of distinct subnet CIDRs advertised by some device but enabled on none — pending approval (exit-node default routes excluded). |
| `tailscale.tailnet_lock.errors` | `1` | gauge | `tailscale_tailnet_lock_errors_ratio` | — | Number of devices with a non-empty tailnet-lock error (a **count**, despite `_ratio`); the only actionable tailnet-lock signal the API exposes (every node carries a lock key regardless of whether tailnet lock is enabled). |
<!-- END GENERATED -->

### Users (`tailscale.users.count`, `tailscale.user.*`, `tailscale.user_invites.count`)

User roll-ups and per-user gauges. Per-user "id dims" = `user_id`, `user_name`.

<!-- BEGIN GENERATED: metrics groups="Users" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.user.connected` | `1` | gauge | `tailscale_user_connected_ratio` | `user_id`, `user_name` | `1` if the user is currently connected, else `0`. |
| `tailscale.user.devices` | `1` | gauge | `tailscale_user_devices_ratio` | `user_id`, `user_name` | Number of devices owned by the user (a **count**). |
| `tailscale.user.last_seen` | `s` | gauge | `tailscale_user_last_seen_seconds` | `user_id`, `user_name` | Unix timestamp the user was last seen. |
| `tailscale.user_invites.count` | `1` | gauge | `tailscale_user_invites_count_ratio` | `tailscale_user_invite_role`, `tailscale_user_invite_delivery` | Outstanding open user invites (a **count**), by role and delivery method. The list-user-invites endpoint returns only open (not yet accepted) invites, so this is a snapshot of pending invitations — not accepted-invite history, which the API does not expose. |
| `tailscale.user_invites.pending_age` | `s` | histogram | `tailscale_user_invites_pending_age_seconds` | `tailscale_user_invite_role` | Distribution of time since Tailscale last emailed each pending invite (a **distribution**). Emitted only for emailed invites — manual-link invites have no delivery timestamp to measure age from, so they're omitted rather than reported as age zero. |
| `tailscale.users.age` | `s` | histogram | `tailscale_users_age_seconds` | — | Distribution of user account age (a **distribution**), i.e. time since each user was created. Users with no reported creation time are omitted rather than reported as age zero. |
| `tailscale.users.count` | `1` | gauge | `tailscale_users_count_ratio` | `tailscale_user_role`, `tailscale_user_status`, `tailscale_user_type` | User count (a **count**), bucketed by role/status/type. |
<!-- END GENERATED -->

### Keys (`tailscale.key.*`, `tailscale.keys.count`)

<!-- BEGIN GENERATED: metrics groups="Keys" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.key.allowed_tags` | `1` | gauge | `tailscale_key_allowed_tags_ratio` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description`, `tailscale_key_owner` | Number of tags an OAuth client's trust credential is restricted to (a **count**; 0 means unrestricted — see `tailscale.key.tag_scope`, #416). Always emitted for OAuth clients (never omitted at 0, unlike most optional-array counts elsewhere: 0 is the security-relevant case here). Gated by `cardinality.per_entity.key`. |
| `tailscale.key.expiry` | `s` | gauge | `tailscale_key_expiry_seconds` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_auth_kind`, `tailscale_key_description`, `tailscale_key_owner`, `tailscale_key_tags` | Unix timestamp a Tailscale key expires; one series per key. |
| `tailscale.key.preauthorized` | `1` | gauge | `tailscale_key_preauthorized_ratio` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description`, `tailscale_key_owner`, `tailscale_key_tags` | Whether an auth key is preauthorized (1) or not (0); one series per auth key. Gated by `cardinality.per_entity.key`. |
| `tailscale.key.scope_class` | `1` | gauge | `tailscale_key_scope_class_ratio` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description`, `tailscale_key_owner`, `tailscale_key_scope_class` | Credential privilege class (info gauge, value 1 for the current class / 0 for the rest), replacing a raw scope count as the blast-radius signal (#415): `none`\|`read`\|`all_read`\|`write`\|`all`, ranked by `internal/tsscope`. A single `all` scope (unrestricted read+write, including future APIs) now reads as `all`, distinct from many narrow `*:read` scopes (`read`) or `all:read` (`all_read`, read-only tailnet-wide). Zero-seeded across every class for each scoped credential. Emitted for the same population as `tailscale.key.scopes` (credentials that carry scopes). Gated by `cardinality.per_entity.key`. |
| `tailscale.key.scopes` | `1` | gauge | `tailscale_key_scopes_ratio` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description`, `tailscale_key_owner` | Number of OAuth scopes granted to a credential (scope-sprawl signal); one series per OAuth-client/API credential. Gated by `cardinality.per_entity.key`. |
| `tailscale.key.tag_scope` | `1` | gauge | `tailscale_key_tag_scope_ratio` | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_description`, `tailscale_key_owner`, `tailscale_key_tag_scope` | Top-level trust-credential tag-authority class (info gauge, value 1 for the current class / 0 for the rest, #416): `none` (no tag restriction — the credential may create devices with ANY tag, the broadest authority) or `restricted` (one or more allowed tags constrain it). This is the credential's OWN tag restriction (wire: top-level `tags`), NOT the tags it auto-applies to devices it creates (see `tailscale.key.tags`, from `capabilities.devices.create.tags`) — the two are unrelated fields. Only OAuth clients carry this restriction. Zero-seeded across both classes. Gated by `cardinality.per_entity.key`. |
| `tailscale.keys.age` | `s` | histogram | `tailscale_keys_age_seconds` | — | Fleet age distribution of tailnet keys, in seconds since `created` (#426). A single bounded histogram across every key with a known Created timestamp — not a per-entity series, so it is unconditional on `cardinality.per_entity.key`. Bucket bounds: `internal/entityage.BucketsSeconds()`. |
| `tailscale.keys.by_owner` | `1` | gauge | `tailscale_keys_by_owner_ratio` | `tailscale_key_owner`, `tailscale_key_type` | Key count (a **count**) bucketed by owning user and type — the "who holds the keys" breakdown. Emitted only for keys with a non-empty owner (userId); stays available when `cardinality.per_entity.key` is off. |
| `tailscale.keys.count` | `1` | gauge | `tailscale_keys_count_ratio` | `tailscale_key_type`, `tailscale_key_auth_kind`, `tailscale_key_revoked`, `tailscale_key_invalid` | Key count (a **count**), bucketed by type/auth_kind/revoked/invalid. |
<!-- END GENERATED -->

> Per-entity gauge gating: the per-device, per-user, and per-key gauges above are gated by
> `cardinality.per_entity.device` / `cardinality.per_entity.user` / `cardinality.per_entity.key` (all **on** by default).
> Set one to `false` to drop that collector's per-entity series and keep only its aggregate
> `*.count` roll-up; the key-expiry **warning log** still fires regardless.

### OAuth Apps (`tailscale.oauth_apps.count`, `tailscale.oauth_app.*`)

Inventory of the tailnet's OAuth applications (device provisioning — alpha API). The collector
idles silently (no error) on tailnets without the feature. App names are operator-chosen labels
gated by `pii_filter.free_text_details`; redirect URIs are decoded only to report their count,
while the URI values and client secrets are never emitted.

<!-- BEGIN GENERATED: metrics groups="OAuth Apps" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.oauth_app.node_attributes` | `1` | gauge | `tailscale_oauth_app_node_attributes_ratio` | `tailscale_oauth_app_id`, `tailscale_oauth_app_name` | Number of custom node attributes an OAuth application is allowed to set; one series per app. |
| `tailscale.oauth_app.redirect_uris` | `1` | gauge | `tailscale_oauth_app_redirect_uris_ratio` | `tailscale_oauth_app_id`, `tailscale_oauth_app_name` | Number of redirect URIs configured for an OAuth application (a **count** — the URI values are never emitted); one series per app with at least one configured. #419. |
| `tailscale.oauth_app.scope_class` | `1` | gauge | `tailscale_oauth_app_scope_class_ratio` | `tailscale_oauth_app_id`, `tailscale_oauth_app_name`, `tailscale_oauth_app_scope_class` | OAuth application privilege class (info gauge, value 1 for the current class / 0 for the rest), the app-side analog of the keys collector's `tailscale.key.scope_class` (#415/#419): `none`\|`read`\|`all_read`\|`write`\|`all`, ranked by `internal/tsscope`. Zero-seeded across every class for every app, including one with no scopes at all. |
| `tailscale.oauth_app.scopes` | `1` | gauge | `tailscale_oauth_app_scopes_ratio` | `tailscale_oauth_app_id`, `tailscale_oauth_app_name` | Number of OAuth scopes granted to an OAuth application (scope-sprawl signal); one series per app. |
| `tailscale.oauth_apps.age` | `s` | histogram | `tailscale_oauth_apps_age_seconds` | — | Fleet age distribution of OAuth applications, in seconds since `created` (#426). A single bounded histogram across every app with a known Created timestamp — not a per-entity series. Bucket bounds: `internal/entityage.BucketsSeconds()`. |
| `tailscale.oauth_apps.count` | `1` | gauge | `tailscale_oauth_apps_count_ratio` | — | Number of OAuth applications registered on the tailnet (a **count**). |
<!-- END GENERATED -->

### Tailscale PAM (`tailscale.pam.*`)

The opt-in PAM collector reads the separate Border0 API with a static read-only service-account
token. It reports Border0-only inventory, configuration shape, quota ceilings and bounded session
telemetry. It deliberately does not restate PAM configuration changes already carried by the audit
collector or the Tailscale Service VIPs and ports already carried by the services collector.
Operator-supplied connector, service and policy names remain subject to the PII filter and the
per-tailnet metric-cardinality limit. Session `result` is an authorization outcome, not connection
health, and the API does not expose grant-layer denials as session rows.

<!-- BEGIN GENERATED: metrics groups="PAM,PAM sessions" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.pam.connector.connected` | `1` | gauge | `tailscale_pam_connector_connected_ratio` | `tailscale_pam_connector_name` | `1` when the named PAM connector currently reports connected, else `0`. Connector names are operator-supplied and subject to the configured metric-cardinality limit. |
| `tailscale.pam.connector.info` | `1` | gauge | `tailscale_pam_connector_info_ratio` | `tailscale_pam_connector_name`, `tailscale_pam_version`, `tailscale_pam_built_date` | Info gauge (constant `1`) carrying the named PAM connector's software version and build date. |
| `tailscale.pam.connector.last_seen_age` | `s` | gauge | `tailscale_pam_connector_last_seen_age_seconds` | `tailscale_pam_connector_name` | Seconds since the named PAM connector was last seen. Connector names are operator-supplied and subject to the configured metric-cardinality limit. |
| `tailscale.pam.connector.plugins` | `1` | gauge | `tailscale_pam_connector_plugins_ratio` | `tailscale_pam_connector_name` | Active plugin count reported by the named PAM connector (a count, despite `_ratio`). |
| `tailscale.pam.connector.sockets` | `1` | gauge | `tailscale_pam_connector_sockets_ratio` | `tailscale_pam_connector_name` | PAM service count reported by the named connector (a count, despite `_ratio`). This is connector-local Border0 state, not Tailscale Service port inventory. |
| `tailscale.pam.connector.tokens` | `1` | gauge | `tailscale_pam_connector_tokens_ratio` | `tailscale_pam_connector_name` | Active token metadata count reported by the named PAM connector (a count, despite `_ratio`). |
| `tailscale.pam.connectors` | `1` | gauge | `tailscale_pam_connectors_ratio` | — | Configured Border0 PAM connector count. |
| `tailscale.pam.identities` | `1` | gauge | `tailscale_pam_identities_ratio` | `tailscale_pam_identity_kind`, `tailscale_pam_identity_role` | PAM identity count by kind and role (a count, despite `_ratio`); service accounts are split by role so tag-mirrored client accounts remain distinguishable. |
| `tailscale.pam.org.plan.info` | `1` | gauge | `tailscale_pam_org_plan_info_ratio` | `tailscale_pam_plan` | Info gauge (constant `1`) carrying the PAM organization's bounded plan slug. |
| `tailscale.pam.org.setting.enabled` | `1` | gauge | `tailscale_pam_org_setting_enabled_ratio` | `tailscale_pam_setting_name` | `1` when the stable named PAM organization setting is enabled, else `0`. |
| `tailscale.pam.policies` | `1` | gauge | `tailscale_pam_policies_ratio` | — | Configured Border0 PAM policy count. |
| `tailscale.pam.policy.setting.enabled` | `1` | gauge | `tailscale_pam_policy_setting_enabled_ratio` | `tailscale_pam_policy_name`, `tailscale_pam_version`, `tailscale_pam_setting_name` | `1` when the stable named boolean is enabled for the PAM policy, else `0`. |
| `tailscale.pam.service.alive` | `1` | gauge | `tailscale_pam_service_alive_ratio` | `tailscale_pam_service_name`, `tailscale_pam_service_type` | `1` when the named PAM service reports alive, else `0`; a Border0 health dimension, not Tailscale Service inventory. Service names are operator-supplied and subject to the configured metric-cardinality limit. |
| `tailscale.pam.service.setting.enabled` | `1` | gauge | `tailscale_pam_service_setting_enabled_ratio` | `tailscale_pam_service_name`, `tailscale_pam_service_type`, `tailscale_pam_setting_name` | `1` when the stable named setting is enabled for the named PAM service, else `0`. Service names are operator-supplied and subject to the configured metric-cardinality limit. |
| `tailscale.pam.services` | `1` | gauge | `tailscale_pam_services_ratio` | `tailscale_pam_service_type` | Configured PAM service count by Border0 socket type (a count, despite `_ratio`); does not restate `tailscale.service.ports`. |
| `tailscale.pam.session.duration` | `s` | histogram | `tailscale_pam_session_duration_seconds` | `tailscale_pam_session_type` | Duration of completed PAM sessions, in seconds. |
| `tailscale.pam.session.events` | `{event}` | counter | `tailscale_pam_session_events_total` | `tailscale_pam_session_event_type`, `tailscale_pam_session_event_status` | Bounded PAM session events observed on newly accepted session records, by event type and status; event metadata is never emitted. |
| `tailscale.pam.sessions` | `{session}` | counter | `tailscale_pam_sessions_total` | `tailscale_pam_service_name`, `tailscale_pam_session_type`, `tailscale_pam_session_authorization_result` | PAM sessions authorized to reach a connector; this is authorization outcome telemetry, not connection-health or access-attempt telemetry. |
| `tailscale.pam.sessions.active` | `{session}` | gauge | `tailscale_pam_sessions_active` | `tailscale_pam_service_name`, `tailscale_pam_session_type` | Active PAM sessions visible in the newest-first polling prefix, identified by an absent end time. |
| `tailscale.pam.sessions.killed` | `{session}` | counter | `tailscale_pam_sessions_killed_total` | `tailscale_pam_service_name`, `tailscale_pam_session_type` | PAM sessions reported as killed by Border0. |
| `tailscale.pam.subscription.limit` | `1` | gauge | `tailscale_pam_subscription_limit_ratio` | `tailscale_pam_limit_name` | Configured PAM subscription limit keyed by its stable limit name (a count, despite `_ratio`). |
<!-- END GENERATED -->

### Settings / ACL / DNS (`tailscale.setting.*`, `tailscale.acl.*`, `tailscale.dns.*`)

<!-- BEGIN GENERATED: metrics groups="Settings,ACL,DNS" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.acl.autoapprovers` | `1` | gauge | `tailscale_acl_autoapprovers_ratio` | `tailscale_acl_autoapprover_kind` | Number of auto-approver entries by kind (routes, exit_node, services) (a **count**, despite `_ratio`). |
| `tailscale.acl.last_audit_change` | `s` | gauge | `tailscale_acl_last_audit_change_seconds` | — | Unix timestamp of the newest classified ACL configuration-audit event, taken from the event's upstream source timestamp and persisted through the checkpoint store. Absent until an ACL audit event has been observed; delayed or backfilled events never move it backwards. |
| `tailscale.acl.last_changed` | `s` | gauge | `tailscale_acl_last_changed_seconds` | — | Unix timestamp when this exporter first observed the current ACL policy ETag. This is an approximate revision-observation time, not the policy modification time: the API exposes no last-modified field and polling can observe a revision late. It survives restarts when the checkpoint store is file-backed; an in-memory or failed checkpoint store resets it on restart. |
| `tailscale.acl.posture_gated_rules` | `1` | gauge | `tailscale_acl_posture_gated_rules_ratio` | `tailscale_acl_section` | Number of rules gated by a device-posture condition (`srcPosture`), per section (a **count**, despite `_ratio`). |
| `tailscale.acl.rules` | `1` | gauge | `tailscale_acl_rules_ratio` | `tailscale_acl_section` | Number of rules per ACL section (a **count**, despite `_ratio`). |
| `tailscale.acl.size` | `By` | gauge | `tailscale_acl_size_bytes` | — | Size of the current ACL policy document, in bytes. |
| `tailscale.acl.ssh_wildcard` | `1` | gauge | `tailscale_acl_ssh_wildcard_ratio` | — | Number of Tailscale SSH rules with a wildcard (`*`) source or destination (a **count**, despite `_ratio`). |
| `tailscale.acl.unrestricted_rules` | `1` | gauge | `tailscale_acl_unrestricted_rules_ratio` | `tailscale_acl_section` | Number of non-deny rules matching any source to any destination (wildcard `src` and `dst`), per section (a **count**, despite `_ratio`). |
| `tailscale.acl.validation.errors` | `1` | gauge | `tailscale_acl_validation_errors_ratio` | — | Count of generic validation errors on the last policy check (a **count**, despite `_ratio`). Distinct from `tailscale.acl.validation.test_failures`: the documented API responses report embedded-test failures separately, so this stays `0` in the common case. |
| `tailscale.acl.validation.ok` | `1` | gauge | `tailscale_acl_validation_ok_ratio` | — | `1` if the tailnet's currently active ACL policy (including any tests embedded in its own `tests` section) validated cleanly on the last check, else `0`. Absent entirely — not `0` — when the validate call itself is unavailable (e.g. the credential lacks `policy_file:read`); see `tailscale2otel.api.availability` for that state. |
| `tailscale.acl.validation.test_failures` | `1` | gauge | `tailscale_acl_validation_test_failures_ratio` | — | Count of failed tests embedded in the policy's own `tests` section, evaluated against the policy's own rules on the last check (a **count**, despite `_ratio`). |
| `tailscale.acl.validation.warnings` | `1` | gauge | `tailscale_acl_validation_warnings_ratio` | — | Count of validation warnings on the last policy check (e.g. a group not syncing from SCIM) (a **count**, despite `_ratio`). |
| `tailscale.acl.wildcard_rules` | `1` | gauge | `tailscale_acl_wildcard_rules_ratio` | `tailscale_acl_section`, `tailscale_acl_position` | Number of non-deny ACL/grant rules with a wildcard (`*`) source or destination, per section and position (a **count**, despite `_ratio`). |
| `tailscale.dns.magic_dns` | `1` | gauge | `tailscale_dns_magic_dns_ratio` | — | `1` if MagicDNS is enabled, else `0`. |
| `tailscale.dns.nameservers.count` | `1` | gauge | `tailscale_dns_nameservers_count_ratio` | — | Number of configured nameservers (a **count**). |
| `tailscale.dns.override_local` | `1` | gauge | `tailscale_dns_override_local_ratio` | — | `1` if Tailscale DNS resolvers override the local OS DNS configuration (`preferences.overrideLocalDNS`), else `0`. |
| `tailscale.dns.resolver` | `1` | gauge | `tailscale_dns_resolver_ratio` | `tailscale_dns_resolver_address`, `tailscale_dns_resolver_kind`, `tailscale_dns_resolver_domain`, `tailscale_dns_resolver_use_with_exit_node` | Info gauge (always `1`) for each configured DNS resolver, labeled by `address`, `kind` (`global`\|`split`), split-DNS `domain` (empty for global), and `use_with_exit_node`. A split-DNS domain configured with a null/empty resolver list still emits one point here with `address` empty, so every domain counted in `tailscale.dns.split_zones.count` has an identifiable series. |
| `tailscale.dns.resolvers.use_with_exit_node` | `1` | gauge | `tailscale_dns_resolvers_use_with_exit_node_ratio` | — | Number of DNS resolvers (global + split-DNS) set to remain in use under an exit node (`useWithExitNode`, Tailscale v1.88.1+; a **count**). |
| `tailscale.dns.search_path` | `1` | gauge | `tailscale_dns_search_path_ratio` | `tailscale_dns_search_path_domain` | Info gauge (always `1`) for each configured DNS search path, labeled by `domain`. |
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
| `tailscale.webhook_endpoint.age` | `s` | histogram | `tailscale_webhook_endpoint_age_seconds` | — | Age distribution of configured webhook endpoints, in seconds since creation. Fleet-level: no per-endpoint attributes. Endpoints whose `created` timestamp is absent are omitted rather than recorded as age `0`, and nothing is emitted when no endpoint reports one. |
| `tailscale.webhook_endpoint.subscriptions` | `1` | gauge | `tailscale_webhook_endpoint_subscriptions_ratio` | `tailscale_webhook_endpoint_id`, `tailscale_webhook_endpoint_provider` | Number of event categories a webhook endpoint is subscribed to (a **count**); one series per endpoint. **Gated** by `cardinality.per_entity.webhook`. The endpoint URL/secret/creator are never emitted. |
| `tailscale.webhook_endpoints.count` | `1` | gauge | `tailscale_webhook_endpoints_count_ratio` | — | Number of configured webhook endpoints (a **count**, despite `_ratio`). Emitted as `0` when the tailnet exposes no webhook surface at all (HTTP 404); a rejected or under-scoped credential emits nothing here — see `tailscale2otel.api.availability`. |
| `tailscale.webhook_endpoints.desired_unrecognized` | `1` | gauge | `tailscale_webhook_endpoints_desired_unrecognized_ratio` | — | Number of `collectors.webhooks.desired_events` entries that are not documented subscription categories (a **count**, despite `_ratio`) — almost always a typo. Kept separate from coverage because a misspelled category would otherwise read as permanently uncovered with nothing naming the cause. Emitted only when `desired_events` is non-empty; the offending strings are on the `tailscale.webhook_endpoints.event_mismatch` log event, never a label. |
| `tailscale.webhook_endpoints.event_desired_covered` | `1` | gauge | `tailscale_webhook_endpoints_event_desired_covered_ratio` | `tailscale_webhook_event` | `1` if an event category listed in `collectors.webhooks.desired_events` has at least one subscribed endpoint, else `0` (a **flag**, despite `_ratio`). Emitted only for configured desired categories, and not at all when the list is empty (no expectation). Unrecognized entries are excluded here and counted by `tailscale.webhook_endpoints.desired_unrecognized` instead. |
| `tailscale.webhook_endpoints.event_subscriptions` | `1` | gauge | `tailscale_webhook_endpoints_event_subscriptions_ratio` | `tailscale_webhook_event` | Number of webhook endpoints subscribed to each documented event category (a **count**, despite `_ratio`). The full 18-value subscription vocabulary is emitted every tick, **zero-seeded**, plus `other` for any future value upstream adds — so a category nobody listens for reads as an explicit `0` rather than a missing series, and the label stays bounded at 19 values. An endpoint listing the same category twice counts once. Alert on `== 0` for the categories your incident response depends on. |
<!-- END GENERATED -->

### Posture integrations (`tailscale.posture_integration*.*`)

Device-posture provider integrations (MDM/EDR such as Intune) and their sync health. Alert on
`tailscale.posture_integration.last_sync` going stale. Provider identifiers
(`clientId`/`tenantId`/`cloudId`) are never emitted.

<!-- BEGIN GENERATED: metrics groups="Posture" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.posture_integration.error` | `1` | gauge | `tailscale_posture_integration_error_ratio` | `tailscale_posture_provider`, `tailscale_posture_integration` | `1` if the integration's last sync reported an error, else `0`; one series per provider/integration. The raw error text is deliberately not emitted as a label (unbounded/possibly sensitive). Pair with `last_sync` — `lastSync` advances even on a failed attempt, so this is the only failure signal. |
| `tailscale.posture_integration.last_sync` | `s` | gauge | `tailscale_posture_integration_last_sync_seconds` | `tailscale_posture_provider`, `tailscale_posture_integration` | Unix timestamp of the integration's last synchronization ATTEMPT (not necessarily successful — the API's `lastSync` advances on every attempt, so pair staleness with `tailscale.posture_integration.error` to detect a failing sync). Emitted only once a sync has occurred. |
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
| `tailscale.logstream.bytes_sent` | `By` | counter | `tailscale_logstream_bytes_sent_total` | `tailscale_logstream_type` | Bytes delivered to the log-stream sink (emitted as the delta of Tailscale's cumulative counter). |
| `tailscale.logstream.configured` | `1` | gauge | `tailscale_logstream_configured_ratio` | `tailscale_logstream_type` | `1` if a log stream is configured for this log type, else `0` (a **flag**, despite `_ratio`). |
| `tailscale.logstream.destination.configured` | `1` | gauge | `tailscale_logstream_destination_configured_ratio` | `tailscale_logstream_log_type`, `tailscale_logstream_destination_type` | `1` when Tailscale reports a configured log-stream destination for the log type, keyed by the bounded destination type. No point is emitted when the configuration lookup is ambiguous or denied. |
| `tailscale.logstream.entries_sent` | `{event}` | counter | `tailscale_logstream_entries_sent_total` | `tailscale_logstream_type` | Log entries delivered to the sink. |
| `tailscale.logstream.error` | `1` | gauge | `tailscale_logstream_error_ratio` | `tailscale_logstream_type` | `1` if the last delivery reported an error, else `0` (a **flag**, despite `_ratio`). The error text is on the `tailscale.logstream.error` LOG event, never a label. |
| `tailscale.logstream.last_activity` | `s` | gauge | `tailscale_logstream_last_activity_seconds` | `tailscale_logstream_type` | Unix timestamp of the most recent delivery activity (alert on staleness). |
| `tailscale.logstream.max_body_requests` | `{request}` | counter | `tailscale_logstream_max_body_requests_total` | `tailscale_logstream_type` | Delivery requests that hit the maximum body size (a SIEM backpressure signal). |
| `tailscale.logstream.requests` | `{request}` | counter | `tailscale_logstream_requests_total` | `tailscale_logstream_type` | Total delivery requests to the sink. |
| `tailscale.logstream.requests_failed` | `{request}` | counter | `tailscale_logstream_requests_failed_total` | `tailscale_logstream_type` | Failed delivery requests to the sink (alert on a sustained rate). |
| `tailscale.logstream.spoofed_entries` | `{event}` | counter | `tailscale_logstream_spoofed_entries_total` | `tailscale_logstream_type` | Log entries rejected as spoofed. |
<!-- END GENERATED -->

### Tailscale Services / VIP (`tailscale.service*.*`)

Tailscale Services (VIP services) and their backing hosts. Service addresses, comments and
annotations are **never emitted**; the optional display name is the only additional service
identity field. The bounded `by_tag` rollup is gated by `collect_tag_rollup` and capped by
`tag_rollup_limit`. The per-service `ports`/`hosts`/`host.info` gauges are gated by
`cardinality.per_entity.service`; `hosts` and `host.info` additionally require `collect_hosts`
(one extra API call per service). `host.info` joins the service-host `tailscale.node.id` to the
devices cache when available.

<!-- BEGIN GENERATED: metrics groups="Services" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.service.host.info` | `1` | gauge | `tailscale_service_host_info_ratio` | `tailscale_service_name`, `tailscale_service_display_name`, `tailscale_node_id`, `tailscale_service_approval`, `tailscale_service_configured`, `host_name`, `host_id`, `os_type`, `os_version`, `tailscale_user`, `tailscale_tags` | Info gauge (constant `1`) for each device backing a Tailscale Service, carrying the service name/display name, approval/configured state, the host's `tailscale.node.id`, and joined device identity when the devices cache contains that node. **Gated** by `collect_hosts` and `cardinality.per_entity.service`. |
| `tailscale.service.hosts` | `1` | gauge | `tailscale_service_hosts_ratio` | `tailscale_service_name`, `tailscale_service_display_name`, `tailscale_service_approval`, `tailscale_service_configured` | Backing-host **count** for a Tailscale Service, bucketed by approval + configured state and carrying its optional display name; one series per service/approval/configured. **Gated** by `collect_hosts` (N+1 calls) and `cardinality.per_entity.service`. |
| `tailscale.service.ports` | `{port}` | gauge | `tailscale_service_ports` | `tailscale_service_name`, `tailscale_service_display_name` | Number of port rules exposed by a Tailscale Service; one series per service, carrying its optional display name. **Gated** by `cardinality.per_entity.service`. |
| `tailscale.services.by_tag` | `1` | gauge | `tailscale_services_by_tag_ratio` | `tailscale_tag` | Number of Tailscale Services carrying each ACL tag (a service with N tags counts in N series). **Gated** by `collect_tag_rollup`; capped by `tag_rollup_limit` with overflow tags folded into `tailscale.tag="__other__"`. |
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
| `tailscale.stream.inflight` | `{request}` | updowncounter | `tailscale_stream_inflight` | — | In-flight HTTP requests currently being processed by the HEC receiver. |
| `tailscale.stream.records` | `{record}` | counter | `tailscale_stream_records_total` | `type` | Records accepted by the HEC stream receiver, by stream type (`flow`/`audit`). |
| `tailscale.stream.rejected` | `{rejection}` | counter | `tailscale_stream_rejected_total` | `reason` | Whole requests rejected by the stream receiver, by reason (`auth` = bad token; `auth_required` = network-reachable with no token; `cross_site` = browser-originated request to the untokened receiver; `too_large` = body over the byte cap; `too_many_records` = over the per-request record cap; `too_many_connections` = over the per-request flow-connection cap; `overloaded` = admission budget full; `unparsable` = nothing JSON-like; `malformed` = structurally corrupt batch; `decode_error` = a known record failed typed decoding; `semantic_invalid` = a decoded flow failed bounded semantic validation; `wal_unavailable` = durable append failed). |
| `tailscale.stream.request.duration` | `s` | histogram | `tailscale_stream_request_duration_seconds` | — | Wall-clock duration of HEC receiver HTTP request handling, in seconds. |
| `tailscale.stream.skipped` | `{record}` | counter | `tailscale_stream_skipped_total` | `reason` | Records extracted from an otherwise-valid request body but never routed to a processor, by reason (`unclassified` = matched neither the flow nor audit shape; `unwrap_drop` = a non-object value, e.g. a scalar/null HEC "event", was dropped while unwrapping the envelope before classification). |
| `tailscale.webhook.duplicates` | `{event}` | counter | `tailscale_webhook_duplicates_total` | — | Webhook events suppressed as duplicate deliveries. |
| `tailscale.webhook.events` | `{event}` | counter | `tailscale_webhook_events_total` | `tailscale_webhook_type` | Webhook events accepted, by Tailscale event type. |
| `tailscale.webhook.inflight` | `{request}` | updowncounter | `tailscale_webhook_inflight` | — | In-flight HTTP requests currently being processed by the webhook receiver. |
| `tailscale.webhook.rejected` | `1` | counter | `tailscale_webhook_rejected_total` | `reason` | Webhook deliveries rejected (e.g. bad HMAC), by reason. |
| `tailscale.webhook.request.duration` | `s` | histogram | `tailscale_webhook_request_duration_seconds` | — | Wall-clock duration of webhook receiver HTTP request handling, in seconds. |
| `tailscale.webhook.schema_drift` | `{event}` | counter | `tailscale_webhook_schema_drift_total` | `field`, `status` | Webhook event schema version observations, by known status. |
<!-- END GENERATED -->

### Object-store ingestion (`tailscale2otel.objectstore.*`)

The third flow-log ingestion path: the export Tailscale writes into an S3-compatible bucket
(`collectors.flowlogs.source: objectstore`). The flow records themselves emit the same
`tailscale.network.flow.*` signals every other path does — these describe the **ingestion**, which is
the part that is otherwise invisible.

Watch `tailscale2otel_objectstore_backlog`: it is the number of objects listed but not yet ingested at
the end of the last cycle. A sustained non-zero value means the bucket is being written faster than
`max_objects` allows it to be read, and ingestion is falling behind.

<!-- BEGIN GENERATED: metrics groups="Object-store ingestion" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale2otel.objectstore.backlog` | `1` | gauge | `tailscale2otel_objectstore_backlog_ratio` | — | Objects listed but not yet ingested at the end of the last cycle. This is a lower bound when tailscale2otel.objectstore.scan.truncated is 1; zero means the examined listing ground is caught up, not necessarily the whole bucket. |
| `tailscale2otel.objectstore.bytes` | `By` | counter | `tailscale2otel_objectstore_bytes_total` | — | Compressed object bytes actually read from the export bucket, including unsuccessful ingestion attempts. |
| `tailscale2otel.objectstore.cursor.age` | `s` | gauge | `tailscale2otel_objectstore_cursor_age_seconds` | — | Age in seconds of the ingestion cursor: the wall clock minus the timestamp this cycle leaves persisted, which is the lower bound of the next cycle's listing window. This is end-to-end ingestion lag, and in a healthy feed it settles near the exporter's write cadence plus one collection interval. It is never absent and never negative. A cold start with no persisted cursor reports the configured initial lookback rather than zero, because the cursor genuinely is that far behind; zero means the cursor sits at or ahead of the current instant, which is only reachable inside the fixed clock-skew allowance. |
| `tailscale2otel.objectstore.decompressed.bytes` | `By` | counter | `tailscale2otel_objectstore_decompressed_bytes_total` | — | Decompressed object bytes consumed by ingestion attempts, including attempts stopped by a configured expansion limit. |
| `tailscale2otel.objectstore.discovered.newest.age` | `s` | gauge | `tailscale2otel_objectstore_discovered_newest_age_seconds` | — | Age in seconds of the newest object the last cycle listed, measured from its key timestamp. This is how fresh the EXPORT's own writes are, independent of whether anything was ingested: objects skipped as already ingested still count, so a caught-up feed keeps reporting. -1 means the cycle listed no object with a usable timestamp — an empty or misconfigured prefix, or an exporter silent for longer than the listing window reaches — and is deliberately distinguishable from a fresh zero-second age, so alert on `> threshold or == -1`. Keys that do not parse (the unrecognized_key skip reason) and keys stamped beyond the clock-skew allowance (future_timestamp) are excluded, so a broken exporter clock cannot pin this at zero. Zero means the newest key is stamped at, or within the skew allowance ahead of, now. |
| `tailscale2otel.objectstore.expansion.limit_failures` | `1` | counter | `tailscale2otel_objectstore_expansion_limit_failures_total` | `limit` | Object-store ingestion attempts stopped by a configured wire-byte, decompressed-byte, or record-count limit. The bounded limit attribute identifies the object or cycle limit that was breached. |
| `tailscale2otel.objectstore.gap.healthy` | `1` | gauge | `tailscale2otel_objectstore_gap_healthy_ratio` | — | Whether object-store ingestion has no unresolved gaps. One is healthy; zero means at least one pending or quarantined object remains. |
| `tailscale2otel.objectstore.gap.oldest.age` | `s` | gauge | `tailscale2otel_objectstore_gap_oldest_age_seconds` | — | Age in seconds of the oldest unresolved object-store gap. Zero when no gaps remain. |
| `tailscale2otel.objectstore.gaps` | `1` | gauge | `tailscale2otel_objectstore_gaps_ratio` | — | Failed object-store objects awaiting retry or operator acknowledgement. This count has no object-key attributes. |
| `tailscale2otel.objectstore.objects` | `1` | counter | `tailscale2otel_objectstore_objects_total` | — | Objects successfully ingested from the flow-log export bucket. |
| `tailscale2otel.objectstore.pending.oldest.age` | `s` | gauge | `tailscale2otel_objectstore_pending_oldest_age_seconds` | — | Age in seconds of the oldest object listed but not yet ingested at the end of the last cycle, measured from its key timestamp. This is BACKLOG latency — how stale the next thing to be processed already is — over exactly the population tailscale2otel.objectstore.backlog counts, so zero here pairs with a zero backlog and means nothing is waiting. Like that backlog it is a lower bound while tailscale2otel.objectstore.scan.truncated is 1. It is NOT tailscale2otel.objectstore.gap.oldest.age: that one ages the oldest FAILED object awaiting retry or acknowledgement, and an object that fails leaves this population for that one, so the two report different objects on purpose. A healthy object deferred by the per-cycle object budget or by a cycle expansion limit stays counted here. |
| `tailscale2otel.objectstore.records` | `1` | counter | `tailscale2otel_objectstore_records_total` | — | Flow-log records decoded from ingested objects. Compare against the flow metrics to see what de-duplication removed. |
| `tailscale2otel.objectstore.request.duration` | `s` | histogram | `tailscale2otel_objectstore_request_duration_seconds` | `operation`, `outcome` | Wall-clock duration of object-store provider calls, by operation and outcome. It times the provider call itself: for `get` that is obtaining the object's reader, NOT streaming, decompressing, and decoding its body — that work is measured by the object, record, and byte counters. Attributed identically to tailscale2otel.objectstore.requests and bounded the same way, so the two divide by the same four series. |
| `tailscale2otel.objectstore.requests` | `1` | counter | `tailscale2otel_objectstore_requests_total` | `operation`, `outcome` | Object-store provider calls, by operation and outcome. TRANSPORT health only, and exactly one data point per call: `error` means the LIST or GET call itself returned an error, never a decode, validation, framing, or per-object limit failure — those are counted by tailscale2otel.objectstore.skipped and the gap metrics. A failed GET is therefore counted once here and once on skipped, which measure different things; a body that fails mid-read counts as a SUCCESSFUL get, because the request succeeded and the read failure is already carried by skipped and the gaps. Both attributes are closed two-value sets, so this metric is at most four series and carries nothing derived from an object key, bucket, endpoint, or error text. |
| `tailscale2otel.objectstore.retries` | `1` | counter | `tailscale2otel_objectstore_retries_total` | — | Object ingestion attempts that retried a previously failed object. The retry is OBJECT-level: an object that fails becomes a durable gap and is attempted again on a later cycle under a bounded backoff, and every one of those later attempts counts one — a first attempt on a newly listed object never does. Rising while tailscale2otel.objectstore.gaps does not fall means an object is failing repeatedly rather than recovering. Quarantined gaps are terminal until an operator intervenes, so they are never retried and never counted. Emitted every cycle, zero included, so a flat line reads as a healthy feed rather than as missing data. |
| `tailscale2otel.objectstore.scan.truncated` | `1` | gauge | `tailscale2otel_objectstore_scan_truncated_ratio` | — | Whether unexamined object-listing ground remains after the last cycle. One means an S3 page was truncated or a listed object was not yet durably handled; zero together with a zero backlog means the current listing window is caught up. |
| `tailscale2otel.objectstore.skipped` | `1` | counter | `tailscale2otel_objectstore_skipped_total` | `reason` | Objects or lines not ingested, by reason. A sustained non-zero `per_cycle_budget` means the per-cycle object cap is holding ingestion behind the bucket. `decode_error` and `semantic_invalid` count individual rows discarded from an object that still completed; `semantic_invalid` marks quarantined flow records, so inspect tailscale.network.data_quality for the bounded reason. `undecodable_object` counts whole objects that decoded no record at all while at least one row failed — the signature of an export whose framing is not newline-delimited records, so treat any non-zero value as a broken feed rather than as corrupt data; each one becomes a retried gap instead of being recorded as ingested. A non-zero `future_timestamp` means objects are named beyond the 5-minute clock-skew allowance and were skipped so they could not push the ingestion cursor past the wall clock; check the exporter's clock. |
<!-- END GENERATED -->

### Node metrics scraper (`tailscale.node.*` + forwarded series)

The scraper emits one curated metric — the per-target health gauge below — and otherwise forwards
every scraped `tailscaled` series **verbatim**. Those forwarded series are runtime-named and are
**not** part of the curated catalog; see the dedicated
[Node metrics scraper](#node-metrics-scraper) section for the forwarding behavior and setup.

<!-- BEGIN GENERATED: metrics groups="Node metrics" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.node.derp.home_region` | `1` | gauge | `tailscale_node_derp_home_region_ratio` | `tailscale_node` | The node's current home DERP region ID (as the gauge value). Curated from tailscaled_home_derp_region_id (raw series still forwarded verbatim). |
| `tailscale.node.health_messages` | `1` | gauge | `tailscale_node_health_messages_ratio` | `tailscale_node`, `tailscale_health_type` | Active tailscaled client health-warning messages, by health type. Curated from tailscaled_health_messages (raw series still forwarded verbatim). |
| `tailscale.node.io` | `By` | counter | `tailscale_node_io_bytes_total` | `tailscale_node`, `network_io_direction`, `tailscale_path` | Bytes carried over the tailnet data plane, by direction and folded path. Curated from tailscaled_{inbound,outbound}_bytes_total (raw series still forwarded verbatim). |
| `tailscale.node.packets` | `{packet}` | counter | `tailscale_node_packets_total` | `tailscale_node`, `network_io_direction`, `tailscale_path` | Packets carried over the tailnet data plane, by direction and folded path. Curated from tailscaled_{inbound,outbound}_packets_total (raw series still forwarded verbatim). |
| `tailscale.node.packets.dropped` | `{packet}` | counter | `tailscale_node_packets_dropped_total` | `tailscale_node`, `network_io_direction`, `tailscale_drop_reason` | Packets dropped on the tailnet data plane, by direction and bounded reason. Curated from tailscaled_{inbound,outbound}_dropped_packets_total (raw series still forwarded verbatim). |
| `tailscale.node.peer_relay.endpoints` | `1` | gauge | `tailscale_node_peer_relay_endpoints_ratio` | `tailscale_node`, `tailscale_peer_relay_state` | Peer-relay endpoints currently configured on this node. Curated from tailscaled_peer_relay_endpoints (raw series still forwarded verbatim). |
| `tailscale.node.peer_relay.io` | `By` | counter | `tailscale_node_peer_relay_io_bytes_total` | `tailscale_node`, `tailscale_peer_relay_transport_in`, `tailscale_peer_relay_transport_out` | Bytes this node forwarded while acting as a peer relay. Curated from tailscaled_peer_relay_forwarded_bytes_total (raw series still forwarded verbatim). |
| `tailscale.node.peer_relay.packets` | `{packet}` | counter | `tailscale_node_peer_relay_packets_total` | `tailscale_node`, `tailscale_peer_relay_transport_in`, `tailscale_peer_relay_transport_out` | Packets this node forwarded while acting as a peer relay. Curated from tailscaled_peer_relay_forwarded_packets_total (raw series still forwarded verbatim). |
| `tailscale.node.up` | `1` | gauge | `tailscale_node_up_ratio` | `tailscale_node` | Per-target scrape health: `1` if the last scrape of that node succeeded, else `0`. |
| `tailscale2otel.nodemetrics.discovery.success` | `1` | gauge | `tailscale2otel_nodemetrics_discovery_success_ratio` | — | 1 if the last dynamic target-discovery refresh succeeded, else 0. Emitted only when discovery is enabled. |
| `tailscale2otel.nodemetrics.discovery.targets` | `{target}` | gauge | `tailscale2otel_nodemetrics_discovery_targets` | — | Active node-metrics scrape targets after the last refresh (static plus discovered). Emitted only when discovery is enabled. |
| `tailscale2otel.nodemetrics.metric_names.dropped` | `1` | counter | `tailscale2otel_nodemetrics_metric_names_dropped_total` | `reason` | Forwarded samples dropped, by reason, because their metric name was not yet seen and the distinct forwarded metric-name budget (node_metrics.max_distinct_metrics) was already exhausted. A sustained non-zero rate means a scrape target is presenting more distinct metric names than the budget allows; check node_metrics.max_distinct_metrics and metric_allow/metric_deny. |
| `tailscale2otel.nodemetrics.scrape.failures` | `1` | counter | `tailscale2otel_nodemetrics_scrape_failures_total` | `reason` | Failed node-metrics scrape attempts, by bounded failure reason. `connection_refused` usually means the node is reachable but tailscaled's metrics listener is unavailable; `timeout` means the target did not answer before the scrape deadline; `missing_endpoint` means the target returned HTTP 404; `http_error` covers other non-2xx HTTP responses; `other` covers malformed targets, TLS/auth/read/parse failures, and unknown transport errors. |
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
| `tailscale.rdns.cache.evictions` | `1` | counter | `tailscale_rdns_cache_evictions_total` | `reason` | Cache entries removed, by reason: expired (swept after their TTL), stale_expired (a positive entry swept after outliving its TTL plus enrichment.reverse_dns.stale_ttl, #297), or purge (manual purge via the admin endpoint). |
| `tailscale.rdns.cache.lookups` | `1` | counter | `tailscale_rdns_cache_lookups_total` | `result` | Reverse-DNS cache hot-path lookups by result: hit (cached PTR name), stale (a positive entry past its TTL but still within enrichment.reverse_dns.stale_ttl, served while one background refresh runs, #297), negative (cached failed lookup), or miss (no cached entry; a background resolution is scheduled). |
| `tailscale.rdns.cache.overflows` | `1` | counter | `tailscale_rdns_cache_overflows_total` | — | Hot-path misses for new addresses that could not be scheduled because the cache was at enrichment.reverse_dns.max_entries. A non-zero rate means the cache is too small. |
| `tailscale.rdns.queries` | `1` | counter | `tailscale_rdns_queries_total` | `result` | Background PTR resolutions sent to the upstream resolver, by result (success or failure). This is the load the cache places on the resolver — it should stay low relative to lookups. |
| `tailscale.rdns.refreshes` | `1` | counter | `tailscale_rdns_refreshes_total` | `result` | Background PTR resolutions triggered by serving a stale name (#297), by result (success or failure). These are a subset of the resolver load already counted in tailscale.rdns.queries, broken out separately from first-sighting queries so a stale-refresh storm is visible on its own. |
<!-- END GENERATED -->

### GeoIP (`tailscale.geoip.*`)

Self-observability for the local GeoIP/ASN enrichment databases (`enrichment.geoip`). Emitted only
when `self_observability.enabled` is true; the admin status page shows the same figures directly from
the databases regardless.

`lookups` accounts for every address the flow path offered: the `skipped` database counts addresses
that never reached a database because they are not globally routable — which is where every tailnet
address lands, since the CGNAT range and the Tailscale ULA are refused by construction.

`database.build_time` is the signal worth alerting on. It is MaxMind's build date, not the download
time, so it catches an updater that has silently stopped working rather than one that is merely
running:

```promql
time() - tailscale_geoip_database_build_time_seconds > 14 * 86400
```

<!-- BEGIN GENERATED: metrics groups="GeoIP" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.geoip.database.build_time` | `s` | gauge | `tailscale_geoip_database_build_time_seconds` | `geoip_database` | Unix timestamp of the loaded GeoIP database's build, per database. This is MaxMind's build date, not when the file was downloaded, so it is the right thing to alert staleness on: `time() - tailscale_geoip_database_build_time_seconds > 14 * 86400` catches an updater that has silently stopped working. Absent for a database that is not loaded. |
| `tailscale.geoip.downloads` | `1` | counter | `tailscale_geoip_downloads_total` | `geoip_edition`, `result` | MaxMind database downloads, by edition and result: `updated` (a newer build was fetched, verified against its published SHA-256, and installed), `unmodified` (the conditional request returned 304 — the healthy steady state, and the thing that keeps a daily updater inside MaxMind's download limit), or `failure`. Emitted only when `enrichment.geoip.download.enabled` is set. |
| `tailscale.geoip.lookups` | `1` | counter | `tailscale_geoip_lookups_total` | `geoip_database`, `result` | GeoIP enrichment lookups, by database (`country`, `asn`, or `skipped`) and result. `hit` means a record was found, `miss` means the loaded database has no record for that address, and `skipped` (always with `geoip.database=skipped`) counts addresses that never reached a database because they are not globally routable — loopback, RFC 1918, link-local, and in particular the Tailscale CGNAT range and ULA, which are never geolocated. A database that is not loaded contributes neither hits nor misses. |
| `tailscale.geoip.reloads` | `1` | counter | `tailscale_geoip_reloads_total` | `geoip_database`, `result` | GeoIP database hot-swaps, by database and result. A `success` means a changed file on disk was loaded and swapped in atomically; a `failure` means the new file could not be read or parsed, in which case the previously loaded database keeps serving. An unchanged file is not counted at all. |
<!-- END GENERATED -->

---

## Kubernetes audit

Kubernetes API requests proxied through the Tailscale operator's API-server proxy, as recorded by
**tsrecorder** and read from its S3 bucket (`collectors.k8s_audit`). Enable it with `enableEvents` in
the `tailscale.com/cap/kubernetes` ACL grant — a **beta** upstream feature with no schema version and
no stability guarantee, which is why `tailscale.k8s.schema_drift` exists.

> **These counters count ATTEMPTS, not outcomes.** The source records carry no response status, no
> latency and no byte count — tsrecorder logs the request as the proxy forwards it, and nothing on the
> way back. Allowed-vs-denied, error rates and latency are therefore **not derivable from this feed at
> all**, and no metric here should be read as implying success. If you need outcomes, you need the
> Kubernetes API server's own audit log, which is a different source entirely.

Cardinality is bounded by construction: every metric attribute is normalized to a closed admit-set
with unknown values folded to `other`, because the user agent, resource names and verbs are all
attacker-controllable. Object names, request paths, label/field selectors, pod and container names and
the raw exec command line are **log attributes only** and never appear on a metric.

`tailscale.k8s.api.exec_sessions` carries `tailscale.k8s.command_class`, a bounded classification of
the exec command line (`interactive_shell`, `recon`, `credential_read`, `package_mgmt`, `net_tool`,
`file_transfer`, `none`, `other`). The verbatim command text is on the log record instead, under
`tailscale.k8s.command`, and is redactable on its own via `pii_filter.command_text` — the class
survives that redaction, so the exec metrics keep working with the raw text switched off.

<!-- BEGIN GENERATED: metrics groups="Kubernetes audit" -->
| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |
|---|---|---|---|---|---|
| `tailscale.k8s.api.exec_sessions` | `1` | counter | `tailscale_k8s_api_exec_sessions_total` | `tailscale_k8s_namespace`, `tailscale_k8s_command_class`, `tailscale_k8s_session_type`, `tailscale_k8s_user` | Interactive exec/attach/portforward requests against a pod, by namespace, bounded command class, session type, and user. command_class summarizes what the command does (interactive_shell, recon, package_mgmt, ...); the raw command text is a log attribute only. |
| `tailscale.k8s.api.mutations` | `1` | counter | `tailscale_k8s_api_mutations_total` | `tailscale_k8s_verb`, `tailscale_k8s_resource`, `tailscale_k8s_namespace`, `tailscale_k8s_user` | Mutating Kubernetes API requests (create/update/patch/delete/deletecollection) proxied through Tailscale, by verb, resource, namespace, and user. Counts the request being made, not that the mutation was admitted or persisted. |
| `tailscale.k8s.api.rbac_probes` | `1` | counter | `tailscale_k8s_api_rbac_probes_total` | `tailscale_k8s_resource`, `tailscale_k8s_namespace`, `tailscale_k8s_user` | SelfSubjectAccessReview/SelfSubjectRulesReview/SubjectAccessReview/TokenReview requests — a client asking what it or another identity is allowed to do — by resource, namespace, and user. A probe being made, not its answer. |
| `tailscale.k8s.api.requests` | `1` | counter | `tailscale_k8s_api_requests_total` | `tailscale_k8s_verb`, `tailscale_k8s_resource`, `tailscale_k8s_subresource`, `tailscale_k8s_api_group`, `tailscale_k8s_namespace`, `tailscale_k8s_user_agent`, `tailscale_k8s_user`, `tailscale_k8s_recorder` | Kubernetes API requests proxied through the Tailscale API-server proxy, by verb, resource, subresource, API group, namespace, user agent, user, and recorder node. Counts attempts only — the source schema carries no response status. |
| `tailscale.k8s.api.sensitive_reads` | `1` | counter | `tailscale_k8s_api_sensitive_reads_total` | `tailscale_k8s_resource`, `tailscale_k8s_namespace`, `tailscale_k8s_user`, `tailscale_k8s_user_agent` | Read (get/list/watch) requests against sensitive resources — secrets, service accounts, RBAC objects, token/CSR reviews — by resource, namespace, user, and user agent. A read being attempted, not that it succeeded or returned data. |
| `tailscale.k8s.schema_drift` | `1` | counter | `tailscale_k8s_schema_drift_total` | `field`, `status` | Kubernetes-audit schema vocabulary observations, by field and whether its value is known to this collector version. The feature is explicitly beta and unversioned upstream, so an unexpected event type or enum value increments this rather than being silently dropped or guessed at. |
| `tailscale.k8s.session.started` | `1` | counter | `tailscale_k8s_session_started_total` | `tailscale_k8s_session_type`, `tailscale_k8s_namespace`, `tailscale_k8s_command_class`, `tailscale_k8s_user` | Recorded terminal sessions started against a Kubernetes pod (from the tsrecorder .cast header), by session type, namespace, bounded command class, and user. Session completeness cannot be observed — there is no documented end-of-recording signal — so this counts starts only. |
<!-- END GENERATED -->

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
| `tailscale.acl.policy_diff` | INFO | `tailscale_acl_etag`, `tailscale_snapshot_kind`, `tailscale_snapshot_reason`, `tailscale_snapshot_revision`, `tailscale_snapshot_emission_id`, `tailscale_snapshot_bytes`, `tailscale_snapshot_seq`, `tailscale_snapshot_total` | Unified diff between the prior and current raw ACL policy bodies, emitted only with collectors.acl.snapshot_enabled on a changed policy revision. Large diffs use the same UTF-8-safe chunk contract as snapshots. The prior body is retained in the configured snapshot state store so the first post-restart revision still has a diff baseline. |
| `tailscale.acl.policy_snapshot` | INFO | `tailscale_acl_etag`, `tailscale_snapshot_kind`, `tailscale_snapshot_reason`, `tailscale_snapshot_revision`, `tailscale_snapshot_emission_id`, `tailscale_snapshot_bytes`, `tailscale_snapshot_seq`, `tailscale_snapshot_total` | Raw ACL policy body, emitted only when collectors.acl.snapshot_enabled is set and the revision changes or its heartbeat is due. Large bodies are UTF-8-safe chunks that MUST be grouped by tailscale.snapshot.emission_id, verified to have one matching tailscale.snapshot.revision, then sorted by tailscale.snapshot.seq before reassembly. |
| `tailscale.acl.risky_rule` | WARN | `tailscale_acl_section`, `tailscale_acl_rule`, `tailscale_acl_risk_class` | Emitted on a policy revision change for each policy-risk finding: unrestricted ACL/grant rules, wildcard SSH rules, or wildcard auto-approvers. Carries `tailscale.acl.risk_class`, `tailscale.acl.section`, and `tailscale.acl.rule` (the offending entries; a free-text attribute droppable via `pii_filter.free_text_details`). |
| `tailscale.acl.validation_issue` | WARN | `tailscale_acl_validation_kind` | Emitted once per validation-issue kind (`error`, `warning`, `test_failure`) whose count is non-zero in the last policy validation. Carries ONLY the bounded `tailscale.acl.validation.kind` attribute — the validator's free-text messages (rule text, usernames, addresses) are deliberately never emitted, not even in the log body. |
| `tailscale.config.audit` | INFO | `tailscale_audit_action`, `tailscale_audit_origin`, `tailscale_audit_event_group_id`, `user_id`, `user_name`, `user_full_name`, `tailscale_actor_type`, `tailscale_target_id`, `tailscale_target_name`, `tailscale_target_type`, `tailscale_target_property`, `tailscale_audit_old`, `tailscale_audit_new`, `tailscale_audit_details`, `error_message`, `tailscale_audit_type`, `tailscale_actor_tags`, `tailscale_target_ephemeral`, `tailscale_audit_deferred_at` | Per configuration-audit event: actor, target, action, and (when present) the before/after change. Emitted at **WARN** when the event carries an error, otherwise INFO. |
| `tailscale.device.attribute.expiring` | WARN | `host_name`, `host_id`, `attribute`, `tailscale_device_attribute_expires_in_days` | Emitted per device+attribute when a posture attribute's expiry falls within the fixed 14-day warn window (and has not yet expired) — the attribute analog of `tailscale.device.key_expiring`, reusing the same lead time. Carries the device hostname, device ID (`host.id`), the expiring attribute key (`attribute`), and remaining days (`tailscale.device.attribute_expires_in_days`). **Gated** by `collect_posture` and the `attribute_namespaces` allow-list. |
| `tailscale.device.change` | INFO | `host_name`, `host_id`, `tailscale_user`, `os_type`, `os_version`, `tailscale_tags`, `tailscale_node_hostname`, `tailscale_client_version`, `tailscale_device_change`, `tailscale_device_field`, `tailscale_audit_old`, `tailscale_audit_new` | Structured device-inventory transition emitted when `collectors.devices.change_log_enabled` is on: one `added` or `removed` record per device, and one `changed` record per material field (name, hostname, OS/version, user, client version, tags, routes, or node-key expiry state). The first successful poll after process start silently establishes the baseline. Current device identity is carried on the classified host/user attributes; before/after values use the existing free-text `tailscale.audit.old`/`tailscale.audit.new` keys and therefore follow `pii_filter.free_text_details`. |
| `tailscale.device.key_expiring` | WARN | `host_name`, `host_id`, `tailscale_device_key_expires_in_days` | Emitted per device when its node key expires within the fixed 14-day warn window (and has not yet expired). Carries the device hostname, device ID (`host.id`), and remaining days (`tailscale.device.key_expires_in_days`). The fleet-wide `tailscale.devices.key_expiry` histogram is always emitted for devices with key expiry enabled; this log adds the per-device actionable signal. |
| `tailscale.device.posture` | INFO | `host_name`, `host_id`, `tailscale_device_posture_details` | Per-device posture/identity snapshot, carrying the device identity plus the posture attributes reported by the API (JSON-encoded under `tailscale.device.posture.details`, gated by `pii_filter.free_text_details`). **Gated** by `collect_posture`; by default emitted only when a device's posture changes (see `posture_log_mode`). |
| `tailscale.device.tailnet_lock_error` | ERROR | `host_name`, `host_id` | Emitted per device when its tailnet-lock error is non-empty (e.g. an unsigned node); the error text is the log body. |
| `tailscale.device_invite` | INFO | `host_name`, `host_id`, `tailscale_user`, `user_name`, `tailscale_device_invite_delivery` | Per-invite log event emitted during device-invite collection (gated by `collect_device_invites`). Carries the invitee email, the login of the user who accepted the invite (when accepted), the bounded delivery state (`tailscale.device_invite.delivery`), and the sharing device identity. Only emitted when at least one of email or acceptedBy.loginName is present on the wire record (anonymous link-only invites that have not been accepted are skipped). `host.id` is the sharing device's device id, consistent with every other device signal (not its nodeId). The invite's `inviteUrl` is a bearer token and is never decoded, let alone emitted. |
| `tailscale.dns.snapshot` | INFO | `tailscale_snapshot_kind`, `tailscale_snapshot_reason`, `tailscale_snapshot_revision`, `tailscale_snapshot_emission_id`, `tailscale_snapshot_bytes`, `tailscale_snapshot_seq`, `tailscale_snapshot_total` | The complete DNS configuration as JSON, emitted only when collectors.dns.snapshot_enabled is set and the configuration changes or its daily heartbeat is due. Large bodies are UTF-8-safe chunks that MUST be grouped by tailscale.snapshot.emission_id and sorted by tailscale.snapshot.seq before reassembly. |
| `tailscale.k8s.api_request` | INFO | `tailscale_k8s_verb`, `tailscale_k8s_resource`, `tailscale_k8s_subresource`, `tailscale_k8s_api_group`, `tailscale_k8s_namespace`, `tailscale_k8s_object_name`, `tailscale_k8s_path`, `tailscale_k8s_label_selector`, `tailscale_k8s_field_selector`, `tailscale_k8s_user_agent`, `tailscale_k8s_user`, `tailscale_k8s_src_node`, `tailscale_k8s_src_node_id`, `tailscale_k8s_recorder`, `tailscale_k8s_command`, `tailscale_k8s_command_class`, `tailscale_k8s_pod`, `tailscale_k8s_container` | Per proxied Kubernetes API request: verb, target resource/object, namespace, requesting user and node, user agent, and (when the request is an exec/attach/portforward) the classified and, unless redacted, raw command. tailscale.k8s.path is the query-free Kubernetes request path — the raw request path/query string is never emitted. |
| `tailscale.k8s.session` | INFO | `tailscale_k8s_session_type`, `tailscale_k8s_namespace`, `tailscale_k8s_pod`, `tailscale_k8s_container`, `tailscale_k8s_command`, `tailscale_k8s_command_class`, `tailscale_k8s_user`, `tailscale_k8s_src_node`, `tailscale_k8s_recorder` | Per recorded terminal session start against a Kubernetes pod: session type, target pod/container/namespace, the classified and, unless redacted, raw launch command, requesting user and node, and the recorder node. Session completeness is not observable, so this fires once at session start only. |
| `tailscale.key.created` | INFO | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_auth_kind`, `tailscale_key_description`, `tailscale_lifecycle_transition`, `tailscale_key_owner`, `tailscale_key_tags` | Emitted once for each key with a non-zero source `created` timestamp. The log timestamp is the source creation time and the observed timestamp is when the successful inventory snapshot was collected. |
| `tailscale.key.expiring` | WARN | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_auth_kind`, `tailscale_key_description`, `tailscale_key_expires_in_seconds`, `tailscale_lifecycle_transition`, `tailscale_key_owner`, `tailscale_key_tags` | Emitted when a key expires within the configured `expiry_warn` window. Carries `tailscale.key.expires_in_seconds` (seconds *until* expiry, a remaining duration — not an absolute timestamp). |
| `tailscale.key.revoked` | INFO | `tailscale_key_id`, `tailscale_key_type`, `tailscale_key_auth_kind`, `tailscale_key_description`, `tailscale_lifecycle_transition`, `tailscale_key_owner`, `tailscale_key_tags` | Emitted once when a key has a non-zero source `revoked` timestamp. The log timestamp is the source revocation time and the observed timestamp is when the successful inventory snapshot was collected; key disappearance or invalid state is not treated as revocation. |
| `tailscale.key.scopes` | INFO | `tailscale_key_id`, `tailscale_key_scope_values`, `tailscale_key_description` | Emitted for each OAuth-client/API credential that carries scopes (scope-sprawl audit log). `tailscale.key.scope_values` is a comma-separated list of the granted capability strings. Gated by `cardinality.per_entity.key`. |
| `tailscale.logstream.error` | ERROR | `tailscale_logstream_type` | Emitted when a log stream's last delivery reported an error; the error text is the log body. |
| `tailscale.network.flow` | INFO | `source_address`, `source_port`, `destination_address`, `destination_port`, `network_transport`, `network_type`, `tailscale_traffic_type`, `tailscale_src_node`, `tailscale_dst_node`, `tailscale_dst_service`, `tailscale_node_id`, `tailscale_node_hostname`, `tailscale_flow_window_start`, `tailscale_flow_window_end`, `tailscale_src_user`, `tailscale_dst_user`, `tailscale_src_tags`, `tailscale_dst_tags`, `tailscale_src_os`, `tailscale_dst_os`, `tailscale_connections`, `tailscale_reporter_trust`, `tailscale_reporter_consistency`, `tailscale_tx_bytes`, `tailscale_rx_bytes`, `tailscale_tx_packets`, `tailscale_rx_packets`, `source_geo_country_iso_code`, `source_geo_continent_code`, `destination_geo_country_iso_code`, `destination_geo_continent_code`, `source_geo_locality_name`, `source_geo_region_iso_code`, `source_geo_location_lat`, `source_geo_location_lon`, `destination_geo_locality_name`, `destination_geo_region_iso_code`, `destination_geo_location_lat`, `destination_geo_location_lon`, `source_as_number`, `source_as_organization_name`, `destination_as_number`, `destination_as_organization_name` | Per-connection (per_connection) or per-record (per_record) network-flow detail: the 5-tuple, transport, traffic type, source/destination node, and tx/rx bytes & packets. With `enrichment.geoip` on, external (non-Tailscale) endpoints also carry geolocation and autonomous-system attributes — the full set, including the ones that are deliberately never allowed onto a metric. |
| `tailscale.oauth_app.info` | INFO | `tailscale_oauth_app_id`, `tailscale_oauth_app_name`, `tailscale_oauth_app_scope_values`, `tailscale_oauth_app_node_attribute_count` | Emitted for each OAuth application on the tailnet. `tailscale.oauth_app.scope_values` is a comma-separated list of the granted scope strings; `tailscale.oauth_app.node_attribute_count` is the number of custom node attributes it may set. |
| `tailscale.pam.session` | INFO | `tailscale_pam_session_socket_name`, `tailscale_pam_session_type`, `tailscale_pam_session_authorization_result`, `tailscale_pam_session_killed`, `tailscale_pam_session_recording_type`, `tailscale_pam_session_duration_seconds`, `user_name`, `user_full_name`, `user_id`, `tailscale_pam_session_client_tailnet_ip`, `tailscale_pam_session_client_external_ip`, `tailscale_pam_session_client_port`, `host_name`, `tailscale_pam_session_command` | One record for each newly accepted PAM session when `collectors.pam.session_log_enabled` is set. `tailscale.pam.session.authorization_result` is the authorization outcome, not connection health: a session can be authorized even if its upstream connection later fails. A tailnet grant-layer denial produces no session row and therefore no record. `tailscale.pam.session.duration_seconds` is a numeric string for complete sessions and `unknown` otherwise. Identity, address, device-name, and command attributes follow the configured `pii_filter` categories; `auth_info` and event metadata are never emitted. |
| `tailscale.pam.snapshot` | INFO | `tailscale_snapshot_kind`, `tailscale_snapshot_reason`, `tailscale_snapshot_revision`, `tailscale_snapshot_emission_id`, `tailscale_snapshot_bytes`, `tailscale_snapshot_seq`, `tailscale_snapshot_total` | Safe PAM inventory and configuration shape, emitted only when collectors.pam.snapshot_enabled is set and the content changes or its heartbeat is due. Upstream authentication objects, passwords, private keys, certificates, usernames and identity details are removed before serialization; large bodies are UTF-8-safe chunks grouped by tailscale.snapshot.emission_id. |
| `tailscale.posture_integrations.snapshot` | INFO | `tailscale_snapshot_kind`, `tailscale_snapshot_reason`, `tailscale_snapshot_revision`, `tailscale_snapshot_emission_id`, `tailscale_snapshot_bytes`, `tailscale_snapshot_seq`, `tailscale_snapshot_total` | The complete safe device-posture integration inventory as JSON, emitted only when collectors.posture_integrations.snapshot_enabled is set and the configuration changes or its daily heartbeat is due. Provider credentials and raw sync-error text are never included; large bodies are UTF-8-safe chunks that MUST be grouped by tailscale.snapshot.emission_id and sorted by tailscale.snapshot.seq before reassembly. |
| `tailscale.settings.snapshot` | INFO | `tailscale_snapshot_kind`, `tailscale_snapshot_reason`, `tailscale_snapshot_revision`, `tailscale_snapshot_emission_id`, `tailscale_snapshot_bytes`, `tailscale_snapshot_seq`, `tailscale_snapshot_total` | The complete safe tailnet-settings configuration as JSON, emitted only when collectors.settings.snapshot_enabled is set and the configuration changes or its daily heartbeat is due. The ACL external-link value is reduced to a presence boolean; large bodies are UTF-8-safe chunks that MUST be grouped by tailscale.snapshot.emission_id and sorted by tailscale.snapshot.seq before reassembly. |
| `tailscale.user_invite.no_longer_open` | INFO | `tailscale_user_invite_id`, `tailscale_user_invite_role`, `tailscale_lifecycle_transition`, `user_id`, `user_name` | Emitted once when an invite present in an earlier successful open-invite snapshot is absent from a later successful snapshot. The API exposes no terminal reason, so absence is not classified as accepted, revoked, or canceled. The invite URL is never retained or emitted; user identity attributes follow the PII filter. |
| `tailscale.user_invite.observed` | INFO | `tailscale_user_invite_id`, `tailscale_user_invite_role`, `tailscale_lifecycle_transition`, `user_id`, `user_name` | Emitted once when an open user invite is first observed in a successful snapshot. This is an observation, not proof of when the invite was created; the API exposes no invite-created timestamp. The invite URL is a bearer token and is never emitted. User identity attributes follow the PII filter. |
| `tailscale.webhook.<type>` | INFO / WARN by type | `tailscale_webhook_type`, `tailscale_tailnet`, `tailscale_webhook_node_id`, `tailscale_webhook_node_device_name`, `tailscale_webhook_node_managed_by`, `tailscale_webhook_actor`, `tailscale_webhook_url`, `tailscale_webhook_key_expiration`, `tailscale_webhook_user`, `tailscale_webhook_old_roles`, `tailscale_webhook_new_roles` | Per webhook event; `<type>` is the Tailscale event type. Emitted at **WARN** for attention-worthy types (node key expiry, needs-approval/authorization/signature, deletions), otherwise INFO. The client-misconfig health events `exitNodeIPForwardingNotEnabled`/`subnetIPForwardingNotEnabled` are INFO and surfaced via the `NodeIPForwardingMisconfigured` alert. |
| `tailscale.webhook_endpoints.event_mismatch` | WARN | `tailscale_webhook_event` | Emitted per `collectors.webhooks.desired_events` entry that is either uncovered (no endpoint subscribes to it) or unrecognized (not a documented category). The body names the reason; the `tailscale.webhook.event` attribute carries the bounded category (`other` for an unrecognized entry). No endpoint URL, creator identity or secret can appear here. |
| `tailscale.webhooks.snapshot` | INFO | `tailscale_snapshot_kind`, `tailscale_snapshot_reason`, `tailscale_snapshot_revision`, `tailscale_snapshot_emission_id`, `tailscale_snapshot_bytes`, `tailscale_snapshot_seq`, `tailscale_snapshot_total` | The complete safe webhook-endpoint inventory as JSON, emitted only when collectors.webhooks.snapshot_enabled is set and the configuration changes or its daily heartbeat is due. Endpoint URLs, secrets, and creator login names are never included; large bodies are UTF-8-safe chunks that MUST be grouped by tailscale.snapshot.emission_id and sorted by tailscale.snapshot.seq before reassembly. |
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

**Choose ONE ingestion source per log type.** For flow and audit logs, use the poller
(`source: poll`), the HEC stream receiver (`source: stream`), or object-store ingestion
(`source: objectstore`). Running more than one
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

---

## Where these definitions come from

This page is **generated from the telemetry catalog in the code**, so it cannot drift from what the
binary actually emits — CI fails the build if the two disagree.

- [`tools/metricscatalog`](https://github.com/rknightion/tailscale2otel/tree/main/tools/metricscatalog) — the generator that writes this page
- [`internal/catalog`](https://github.com/rknightion/tailscale2otel/tree/main/internal/catalog) — the metric and log-event descriptors themselves
- [`internal/semconv`](https://github.com/rknightion/tailscale2otel/tree/main/internal/semconv) — attribute-name constants
- [`deploy/grafana`](https://github.com/rknightion/tailscale2otel/tree/main/deploy/grafana) — dashboards built on these signals
- [`deploy/alerts`](https://github.com/rknightion/tailscale2otel/tree/main/deploy/alerts) — shipped Prometheus and Grafana alert rules

The running exporter also serves this same catalog live on its admin status page, alongside the
active-series cardinality for the last export interval.

Spotted a signal that is wrong, missing, or badly named?
[Open an issue](https://github.com/rknightion/tailscale2otel/issues/new) — metric naming is a
one-way door once dashboards depend on it, so corrections are genuinely welcome.
