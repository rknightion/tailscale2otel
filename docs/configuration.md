# Configuration Reference

This is the exhaustive, per-key reference for the `tailscale2otel` YAML configuration. It is the
companion to two other docs:

- **[`config.example.yaml`](../config.example.yaml)** — the canonical, commented example. Copy it,
  edit, and you have a working config; it is the fastest way to get started.
- **[`docs/metrics.md`](./metrics.md)** — every metric and log signal the exporter emits (and the
  OTLP→Prometheus name normalization you query in Grafana Cloud).

Use this page when you need the precise meaning, default, valid values, and gotchas of a specific
setting.

> This file is **hand-maintained** (unlike `docs/metrics.md`, which is generated). If you change the
> config schema in `internal/config/`, update this page too.

## Conventions

- **Default** is the value used when you omit the key. Loading starts from the built-in defaults
  ([`internal/config/defaults.go`](../internal/config/defaults.go)) and overlays your YAML on top, so
  any key you leave out keeps its default.
- **`${ENV}` expansion** — every string value supports `${VAR}` / `$VAR` expansion from the process
  environment. Keep secrets (tokens, client secrets, passwords) in environment variables, not in the
  YAML file. An unset variable expands to an empty string.
- **Durations** use Go's syntax: `500ms`, `30s`, `5m`, `1h`, `168h` (= 7 days).
- **Validation** — invalid enum values and inconsistent combinations are rejected at startup by
  `Config.Validate()` (the exporter refuses to start). Softer issues are surfaced as startup
  **WARN** advisories by `Config.Warnings()` but do not block startup. Both are noted below.

## Contents

- [Top level](#top-level)
- [`tailscale` — API connection & authentication](#tailscale--api-connection--authentication)
- [`otlp` — the OTLP exporter](#otlp--the-otlp-exporter)
- [`enrichment` — device-name cache](#enrichment--device-name-cache)
- [`cardinality` — metric/label cardinality controls](#cardinality--metriclabel-cardinality-controls)
- [`collectors` — per-source polling](#collectors--per-source-polling)
- [`checkpoint` — poll high-water marks](#checkpoint--poll-high-water-marks)
- [`streaming` — Splunk-HEC log receiver](#streaming--splunk-hec-log-receiver)
- [`webhook` — event webhook receiver](#webhook--event-webhook-receiver)
- [`self_observability` — the exporter's own telemetry](#self_observability--the-exporters-own-telemetry)
- [`admin` — admin HTTP server (probes + status page)](#admin--admin-http-server-probes--status-page)
- [`profiling` — pprof & Pyroscope](#profiling--pprof--pyroscope)

---

## Top level

| Key | Default | Description |
|-----|---------|-------------|
| `log_level` | `info` | Logging verbosity. One of `debug`, `info`, `warn`, `error`. |

---

## `tailscale` — API connection & authentication

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.tailnet` | `example.com` (placeholder) | Your tailnet's name, or `-` for the default tailnet of the authenticating principal. Set this — the built-in default is a placeholder. |

### `tailscale.auth`

Prefer OAuth: its tokens are short-lived, auto-refreshing, and not bound to a user.

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.auth.method` | `oauth` | Authentication method. One of `oauth` (recommended) or `apikey`. |
| `tailscale.auth.oauth.client_id` | `""` | OAuth client ID. Required when `method: oauth`. |
| `tailscale.auth.oauth.client_secret` | `""` | OAuth client secret. Required when `method: oauth`; keep it in an env var. |
| `tailscale.auth.oauth.scopes` | `["all:read"]` | OAuth scopes requested for the token. Least-privilege read scopes are the default; add `log_streaming` if you use `streaming.auto_configure`. |
| `tailscale.auth.oauth.token_url` | `https://api.tailscale.com/api/v2/oauth/token` | Token endpoint. Rarely changed. |
| `tailscale.auth.apikey` | `""` | Personal API key. Used **only** when `method: apikey`. |

> **WARN (advisory):** `method: apikey` triggers a startup warning — a personal API key expires in
> ≤90 days and stops working when the user who created it is suspended or removed. For an unattended
> exporter, prefer `method: oauth`.

### `tailscale.http`

The HTTP client used for all Tailscale API calls.

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.http.timeout` | `30s` | Per-request timeout. |
| `tailscale.http.retry.max_attempts` | `4` | Maximum attempts per request (initial try + retries) under exponential backoff. |
| `tailscale.http.retry.base_delay` | `500ms` | Initial backoff delay. |
| `tailscale.http.retry.max_delay` | `10s` | Maximum backoff delay between retries. |
| `tailscale.http.rate_limit` | `0` | Global request rate cap in requests/second across **all** collectors. `0` = unlimited. |

---

## `otlp` — the OTLP exporter

The single egress path for metrics and logs. `internal/telemetry` is the only component that touches
OTLP.

| Key | Default | Description |
|-----|---------|-------------|
| `otlp.protocol` | `http` | Transport. One of `grpc`, `http`, or `stdout`. `stdout` prints signals to the console for local debugging (no backend, no network). |
| `otlp.endpoint` | `https://otlp-gateway-prod-us-central-0.grafana.net/otlp` | OTLP endpoint base URL (ignored when `protocol: stdout`). For Grafana Cloud use the `…/otlp` base — the per-signal `/v1/metrics` and `/v1/logs` paths are appended for you. |
| `otlp.metric_interval` | `60s` | How often metrics are pushed. `60s` aligns with the default 1 data-point-per-minute scrape cadence and avoids Grafana Cloud DPM churn. |
| `otlp.headers` | `{}` | Extra raw headers added to every OTLP request (an alternative to `grafana_cloud`). |

### `otlp.grafana_cloud`

Convenience for Grafana Cloud: when both are set, an `Authorization: Basic <base64(instance:token)>`
header is built for you (no need to hand-craft it in `otlp.headers`).

| Key | Default | Description |
|-----|---------|-------------|
| `otlp.grafana_cloud.instance_id` | `""` | Grafana Cloud OTLP instance/stack ID (the Basic-auth username). |
| `otlp.grafana_cloud.token` | `""` | Grafana Cloud OTLP token (the Basic-auth password). Keep it in an env var. |

### `otlp.tls`

Transport security for `grpc`/`http`.

| Key | Default | Description |
|-----|---------|-------------|
| `otlp.tls.insecure` | `false` | Disable transport security (plaintext). Use only for a trusted local Collector. |
| `otlp.tls.ca_file` | `""` | Path to a CA bundle to trust for the server certificate. |
| `otlp.tls.cert_file` | `""` | Client certificate (for mTLS). |
| `otlp.tls.key_file` | `""` | Client private key (for mTLS). |

---

## `enrichment` — device-name cache

The in-memory IP/nodeID→name cache, populated by the `devices` collector and used to enrich flow and
audit records.

| Key | Default | Description |
|-----|---------|-------------|
| `enrichment.cache_ttl` | `5m` | Staleness-alarm threshold for the device cache. If the cache hasn't refreshed within this window, a staleness signal is raised. |

> Enrichment depends on the `devices` collector. If `devices` is disabled, flow/audit IP→name
> resolution silently degrades to `unknown`/`external`.

---

## `cardinality` — metric/label cardinality controls

These knobs trade detail for active-series count. They apply to the shared processors, so they take
effect no matter whether logs arrive by poll or by stream.

| Key | Default | Description |
|-----|---------|-------------|
| `cardinality.flow_source_port` | `false` | Add `source.port` as an attribute on flow **metrics** (raw families only). Ports are always present on flow **logs** regardless. On = higher cardinality. |
| `cardinality.flow_destination_port` | `false` | Add `destination.port` as an attribute on flow **metrics** (raw families only). On = higher cardinality. |
| `cardinality.flow_destination_service` | `false` | Add `tailscale.dst.service` (the IANA name for the destination port+transport, e.g. `tcp/443`→`https`) on flow **metrics** — a bounded, low-cardinality stand-in for the destination port. |
| `cardinality.flow_node_dims` | `true` | Include source/destination device names (`tailscale.src.node`/`tailscale.dst.node`) on flow metrics. |
| `cardinality.collapse_external` | `true` | Bucket unresolved/off-tailnet IPs as `external`/`unknown` instead of the raw address (on both flow logs and, when `flow_node_dims` is on, the flow-metric node labels). Off = one series per distinct external IP. |
| `cardinality.device_per_entity` | `true` | Emit per-device gauges (online, last-seen, key-expiry, DERP latency, routes). `false` drops them, leaving only the aggregate `tailscale.devices.count`. |
| `cardinality.user_per_entity` | `true` | Emit per-user gauges (devices, connected, last-seen). `false` leaves only `tailscale.users.count`. |
| `cardinality.key_per_entity` | `true` | Emit the per-key expiry gauge. `false` leaves only `tailscale.keys.count` (the "expiring soon" WARN log still fires). |

---

## `collectors` — per-source polling

Each collector has at least `enabled` and `interval`. The two **log** collectors (`flowlogs`,
`auditlogs`) additionally have `source` and a set of windowing fields; the rest are point-in-time
snapshots.

### Common fields

| Key | Applies to | Default | Description |
|-----|-----------|---------|-------------|
| `<collector>.enabled` | all | `true` (except `node_metrics`) | Whether the collector runs. |
| `<collector>.interval` | all | per-collector | Poll cadence. Snapshot collectors read once per interval; window (log) collectors poll one time-window per interval. |

### `source` and the windowing fields (`flowlogs` / `auditlogs` only)

`source` selects how the log collector obtains data:

- **`poll`** (default) — the exporter pulls logs from the Tailscale API on `interval`, one
  time-window per tick.
- **`stream`** — logs are **pushed** to the [`streaming`](#streaming--splunk-hec-log-receiver)
  receiver instead; the exporter does not poll this log type.
- **`both`** — poll *and* accept the stream. **Discouraged:** the same record can be double-counted.
  Cross-source de-duplication is a best-effort failsafe, not a guarantee, and a startup WARN fires.

Pick exactly one method per log type. Which fields are honored depends on `source`:

| Field | Applies to | `poll` | `stream` | Purpose |
|-------|-----------|:------:|:--------:|---------|
| `enabled` | both | ✓ | ✓ | Turn the collector on/off. |
| `source` | both | ✓ | ✓ | Select the ingestion path. |
| `interval` | both | ✓ | — | Poll cadence (no poller runs under `stream`). |
| `lag` | both | ✓ | — | Query only up to `now − lag`, so late-arriving records aren't missed. |
| `initial_lookback` | both | ✓ | — | Cold-start reach-back when there is no checkpoint yet. |
| `max_window` | both | ✓ | — | Cap a single tick's window so a long outage catches up over several ticks. |
| `log_mode` | `flowlogs` | ✓ | ✓ | Log detail level — output shaping in the shared processor. |
| `max_log_records_per_window` | `flowlogs` | ✓ | ✓¹ | Cap on emitted flow LOG records (see below). |

¹ Under `poll` the budget is shared across the whole poll window; under `stream` it is applied per
received record. Either way, **metrics are never capped — only logs.**

> The four windowing fields exist purely to drive the poller, so they are **ignored when
> `source: stream`**. The `streaming`/`webhook` receivers and the pollers feed the *same* processors,
> which is why `log_mode` and the `cardinality.*` knobs apply on every path.

### `collectors.devices`

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.devices.enabled` | `true` | Emit device gauges + counts and **populate the enrichment cache**. |
| `collectors.devices.interval` | `60s` | Poll cadence. |
| `collectors.devices.collect_routes` | `false` | Also emit per-device subnet-route gauges. Read from the inline device data — **no extra API call**. |
| `collectors.devices.collect_posture` | `false` | Also fetch device posture attributes (one **extra API call per device per tick**) and emit a posture log event per device. |

### `collectors.flowlogs`

Network flow logs → aggregated traffic counters + per-connection flow logs.

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.flowlogs.enabled` | `true` | Whether flow logs are collected. |
| `collectors.flowlogs.source` | `poll` | `poll` \| `stream` \| `both`. See [`source`](#source-and-the-windowing-fields-flowlogs--auditlogs-only). |
| `collectors.flowlogs.interval` | `60s` | Poll cadence (poll only). |
| `collectors.flowlogs.lag` | `120s` | Tail-safety margin; query up to `now − lag` (poll only). Flow logs have a noticeable tail, hence the larger default than audit. |
| `collectors.flowlogs.initial_lookback` | `5m` | Cold-start reach-back (poll only). |
| `collectors.flowlogs.max_window` | `1h` | Catch-up cap for one tick (poll only). |
| `collectors.flowlogs.log_mode` | `per_connection` | Flow-log detail. One of `per_connection` (one log per 5-tuple), `per_record` (one summary log per node window), or `off` (no flow logs, metrics only). |
| `collectors.flowlogs.max_log_records_per_window` | `0` | Cap on flow LOG records emitted (`0` = unlimited). Excess is counted into `tailscale.network.flow.logs_dropped`. Metrics are never capped. |

### `collectors.auditlogs`

Configuration/audit events → event logs + a counter.

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.auditlogs.enabled` | `true` | Whether audit logs are collected. |
| `collectors.auditlogs.source` | `poll` | `poll` \| `stream` \| `both`. See [`source`](#source-and-the-windowing-fields-flowlogs--auditlogs-only). |
| `collectors.auditlogs.interval` | `60s` | Poll cadence (poll only). |
| `collectors.auditlogs.lag` | `60s` | Tail-safety margin (poll only). |
| `collectors.auditlogs.initial_lookback` | `5m` | Cold-start reach-back (poll only). |
| `collectors.auditlogs.max_window` | `6h` | Catch-up cap for one tick (poll only). |

### Snapshot collectors

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.users.enabled` / `.interval` | `true` / `300s` | User/role/status counts and per-user device & connection gauges. |
| `collectors.keys.enabled` / `.interval` | `true` / `300s` | Auth-key expiry gauges, counts, and an "expiring soon" WARN log. |
| `collectors.keys.expiry_warn` | `168h` | Emit the "expiring soon" WARN log when a key expires within this window (default 7 days). |
| `collectors.settings.enabled` / `.interval` | `true` / `600s` | Tailnet feature-toggle gauges. |
| `collectors.acl.enabled` / `.interval` | `true` / `600s` | ACL size + a "policy changed" signal (detected by ETag). |
| `collectors.dns.enabled` / `.interval` | `true` / `600s` | Nameserver / search-path / split-zone counts and the MagicDNS flag. |

### `collectors.node_metrics`

Optional scraper that pulls `tailscaled` per-node Prometheus `/metrics` and forwards them centrally
over OTLP (counters as deltas, gauges as gauges, plus a per-target `tailscale.node.up`). **Off by
default**, and inert unless it has at least one static target or discovery enabled. Node identity is
carried as the `instance` label, not an OTEL Resource. See **[`docs/node-metrics.md`](./node-metrics.md)**
for the operator how-to.

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.node_metrics.enabled` | `false` | Master switch. Even when `true`, the scraper only runs if `targets` is non-empty **or** `discovery.enabled` is `true`. |
| `collectors.node_metrics.interval` | `60s` | Scrape cadence. |
| `collectors.node_metrics.timeout` | `10s` | Per-target scrape timeout. |
| `collectors.node_metrics.max_response_bytes` | `4194304` (4 MiB) | Per-target response-size cap. Must be `> 0` when enabled. |
| `collectors.node_metrics.max_samples` | `50000` | Per-target sample cap per scrape. Must be `> 0` when enabled. |
| `collectors.node_metrics.metric_allow` | `[]` | Anchored regexes on the forwarded metric **name**; if non-empty, a name must match one to be forwarded. Must compile. |
| `collectors.node_metrics.metric_deny` | `[]` | Anchored regexes; a name matching any is dropped (applied after `metric_allow`). Must compile. |
| `collectors.node_metrics.drop_labels` | `[]` | Label keys stripped from every forwarded series. `instance` is never dropped. |

These filters apply **only** to forwarded samples — never to `tailscale.node.up` or the `discovery.*`
gauges.

#### `collectors.node_metrics.targets[]`

A static list of endpoints to scrape (keys below are relative to each list entry). Native
`tailscaled` endpoints are plain HTTP and need no auth/TLS; the optional fields cover proxied/HTTPS
targets.

| Key | Default | Description |
|-----|---------|-------------|
| `url` | — (required) | Scrape URL, e.g. `http://100.64.0.10:5252/metrics`. Required for each target when the scraper is enabled. |
| `instance` | URL `host:port` | Overrides the `instance` label. |
| `labels` | `{}` | Extra static labels merged onto every series from this target. |
| `bearer_token` | `""` | Static bearer token sent as `Authorization: Bearer …`. |
| `bearer_token_file` | `""` | Path read fresh each scrape; takes precedence over `bearer_token`. |
| `headers` | `{}` | Extra request headers (e.g. `X-Scope-OrgID`). |
| `tls.ca_file` / `tls.cert_file` / `tls.key_file` / `tls.server_name` | `""` | TLS trust/identity for HTTPS targets. |
| `tls.insecure_skip_verify` | `false` | Skip server-cert verification (footgun guard defaults off). |

#### `collectors.node_metrics.discovery`

Discover scrape targets dynamically from the Tailscale devices API (keys below are relative to the
`discovery` block). Discovered targets are unioned (deduped by URL, static wins) with the static
`targets`, on this block's **own** interval.

| Key | Default | Description |
|-----|---------|-------------|
| `enabled` | `false` | Turn on dynamic discovery. |
| `interval` | `5m` | How often the devices API is polled for targets (independent of the scrape `interval`). Must be `> 0`. |
| `max_targets` | `1000` | Cap on discovered targets per refresh. Must be `> 0`. |
| `scheme` | `http` | `http` \| `https`. The metrics-endpoint scheme applied to each device. |
| `port` | `5252` | Metrics port (1–65535). |
| `path` | `/metrics` | Metrics path. |
| `online_only` | `true` | Only devices currently connected to the control plane. |
| `exclude_external` | `true` | Skip shared/external devices. |
| `include_tags` | `[]` | If non-empty, only devices with one of these tags (e.g. `["tag:server"]`). |
| `exclude_tags` | `[]` | Devices with any of these tags are skipped (wins over `include_tags`). |
| `address_order` | `ipv4` | Preferred address family, `ipv4` \| `ipv6` (falls back to the other). |
| `instance_source` | `address` | `instance` label source: `address` (host:port), `name` (MagicDNS), or `hostname`. |
| `include_host_labels` | `true` | Attach `host.name`/`host.id` for joins with `tailscale.device.*`. |
| `include_tags_label` | `true` | Attach `tailscale.tags`. |

---

## `checkpoint` — poll high-water marks

Checkpoints record how far each **polled** log collector (`flowlogs`/`auditlogs` with
`source: poll` or `both`) has read, so a restart resumes without gaps or large overlaps. The store is
**unused** if you stream both log types or disable them.

| Key | Default | Description |
|-----|---------|-------------|
| `checkpoint.store` | `memory` | `memory` \| `file`. See below. |
| `checkpoint.file_path` | `/var/lib/tailscale2otel/checkpoints.json` | Where the file store persists. Used only when `store: file`. An empty value silently falls back to `memory`. |

- **`memory`** — the high-water mark lives in RAM only and is lost on restart. After a restart the
  poller cold-starts from `initial_lookback`, so any **downtime longer than `initial_lookback` leaves
  a gap**. Needs no volume; fine for streamed or stateless deployments where the checkpoint is unused
  or disposable.
- **`file`** — the high-water mark is persisted to `file_path` with an atomic write on each tick and
  reloaded at startup, so polling **resumes from the exact high-water mark** across restarts (minor
  boundary overlap is de-duplicated). Use this when you poll logs and want continuity. It needs a
  writable, **persistent** path — e.g. a mounted volume in Kubernetes/Docker.

---

## `streaming` — Splunk-HEC log receiver

Optional receiver for Tailscale's log streaming (a Splunk-HEC sink). When you enable it, set the
relevant log collector(s) to `source: stream` so each log type is ingested by exactly one path.
**Off by default.**

| Key | Default | Description |
|-----|---------|-------------|
| `streaming.enabled` | `false` | Run the HEC receiver. |
| `streaming.listen` | `:8088` | Listen address. |
| `streaming.path` | `/services/collector/event` | HEC event path. |
| `streaming.token` | `""` | Expected as `Authorization: Splunk <token>`. Keep it in an env var. |
| `streaming.public_url` | `""` | Externally reachable receiver URL. **Required when `auto_configure: true`** (it is the sink URL registered with Tailscale). |
| `streaming.tls.cert_file` / `.key_file` | `""` | HTTPS is required by Tailscale; a `tailscale cert` works for private tailnet endpoints. |
| `streaming.decompress` | `auto` | Request-body decompression: `auto` \| `gzip` \| `zstd` \| `none`. |
| `streaming.auto_configure` | `false` | On startup, PUT this receiver as a Splunk-HEC log-streaming sink. **Requires `enabled: true`, `public_url`, and an OAuth client with the `log_streaming` scope.** |
| `streaming.max_body_bytes` | `0` | Cap on the **decompressed** request body. `0` selects a 64 MiB default; a negative value disables the cap. An over-cap POST is rejected with HTTP 413. |

> **Validation:** `auto_configure: true` errors at startup unless both `streaming.enabled: true` and
> a non-empty `streaming.public_url` are set. Running the poller and this receiver for the same log
> type triggers a dual-ingestion **WARN**.

---

## `webhook` — event webhook receiver

Optional receiver for real-time Tailscale events (HMAC-verified). **Off by default.**

| Key | Default | Description |
|-----|---------|-------------|
| `webhook.enabled` | `false` | Run the webhook receiver. |
| `webhook.listen` | `:8089` | Listen address. |
| `webhook.path` | `/tailscale/webhook` | Webhook path. |
| `webhook.secret` | `""` | Shared secret for HMAC-SHA256 verification. Keep it in an env var. |
| `webhook.dedup_audit_events` | `false` | Best-effort: drop a webhook event already counted via the audit logs (shares a cross-source de-dup set with the audit processor). |

---

## `self_observability` — the exporter's own telemetry

| Key | Default | Description |
|-----|---------|-------------|
| `self_observability.enabled` | `true` | Emit the exporter's own health metrics (`tailscale2otel.*`: scrape duration/success/errors, API requests/retries, cardinality, …). |
| `self_observability.instance_id` | `""` | Sets the `service.instance.id` resource attribute so multiple exporter instances are distinguishable. Empty falls back to the host name. Supports `${ENV}`, e.g. `"${POD_NAME}"`. |

---

## `admin` — admin HTTP server (probes + status page)

Always-off-by-default HTTP server exposing liveness/readiness probes plus an optional status page.
The status page surfaces operational metadata (collector health, cardinality, discovered nodes,
**redacted** config) but never secret values. Bind it to a tailnet/loopback address, not the public
internet.

| Key | Default | Description |
|-----|---------|-------------|
| `admin.enabled` | `false` | Run the admin server (`/healthz`, `/readyz`, and — unless disabled — the status page). |
| `admin.listen` | `:9090` | Listen address. For defense-in-depth bind to loopback (`127.0.0.1:9090`) or a tailnet IP. |
| `admin.landing_page` | `true` | Serve the human status page at `/` and machine-readable `/api/status.json`. |
| `admin.auth.token` | `""` | When set, the status page and pprof require this token as the HTTP Basic password (browsers prompt) **or** `Authorization: Bearer <token>`. `/healthz` and `/readyz` are never gated. Keep it in an env var. |

> **WARN (advisory):** if `landing_page` is served on a wildcard (all-interfaces) bind with no
> `admin.auth.token`, a startup warning fires — the page exposes internal state to anyone who can
> reach the port. Set a token or bind to loopback/tailnet.

---

## `profiling` — pprof & Pyroscope

Optional continuous/on-demand profiling. Everything here is **off by default** and carries no
Tailscale data. The pprof handlers mount on the admin server.

| Key | Default | Description |
|-----|---------|-------------|
| `profiling.pprof.enabled` | `false` | Mount `net/http/pprof` handlers on the admin server so Alloy's `pyroscope.scrape` (or `go tool pprof`) can pull profiles. |
| `profiling.pyroscope.enabled` | `false` | Run the Pyroscope continuous-profiling push agent. |
| `profiling.pyroscope.server_address` | `""` | Pyroscope/Grafana Cloud Profiles URL. **Required when `pyroscope.enabled`.** |
| `profiling.pyroscope.basic_auth_user` | `""` | Grafana Cloud: the profiles instance ID (Basic-auth user). |
| `profiling.pyroscope.basic_auth_password` | `""` | Grafana Cloud: an access-policy token with `profiles:write` (Basic-auth password). Keep it in an env var. |
| `profiling.pyroscope.tenant_id` | `""` | `X-Scope-OrgID` for multi-tenant servers (leave empty for Grafana Cloud). |
| `profiling.pyroscope.upload_rate` | `15s` | How often profiles are flushed to the server. |
| `profiling.pyroscope.tags` | `{}` | Extra static labels merged onto every profile, e.g. `{ env: prod }`. |
| `profiling.mutex_profile_fraction` | `0` | `runtime.SetMutexProfileFraction` (`0` = disabled). Mutex profiles stay empty until set. |
| `profiling.block_profile_rate` | `0` | `runtime.SetBlockProfileRate` (`0` = disabled). Block profiles stay empty until set. |

> **Validation / advisories:**
> - `pprof.enabled` errors at startup unless `admin.enabled: true` **and** `admin.auth.token` is set
>   (heap/goroutine dumps can expose in-memory secrets, so pprof must not be served unauthenticated).
> - `pyroscope.enabled` errors at startup without `pyroscope.server_address`.
> - A `grafana.net` `server_address` with an empty `basic_auth_password` triggers a **WARN** —
>   Grafana Cloud Profiles requires the Basic-auth credentials.
