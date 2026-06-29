---
title: Configuration
description: Full key-by-key configuration reference for tailscale2otel — layered defaults, YAML, and TS2OTEL_* environment variables
---

# Configuration Reference

This is the exhaustive, per-key reference for `tailscale2otel` configuration. It is the companion to
two other docs:

- **[`config.example.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml)** — a commented starter showing the common knobs.
  The fastest way to get started.
- **[`docs/metrics.md`](./metrics.md)** — every metric and log signal the exporter emits (and the
  OTLP→Prometheus name normalization you query in Grafana Cloud).

Use this page when you need the precise meaning, default, valid values, and gotchas of a specific
setting.

> This file is **hand-maintained** (unlike `docs/metrics.md`, which is generated). If you change the
> config schema in `internal/config/`, update this page too.

## Layered configuration

Configuration is loaded in three layers, lowest precedence first:

1. **Built-in defaults** — the exporter runs without a config file; any key you do not set keeps its
   default (defined in [`internal/config/defaults.go`](https://github.com/rknightion/tailscale2otel/blob/main/internal/config/defaults.go)).
2. **YAML file** (optional) — pass `-config path/to/file.yaml`; the file overrides defaults for any
   key it mentions. A non-existent path passed with `-config` is an error; omitting `-config`
   entirely is not.
3. **Environment variables** — highest precedence; override both defaults and the file.

## Environment-variable convention

Every config field is settable via an environment variable:

- **Prefix:** `TS2OTEL_`
- **Nesting delimiter:** `__` (double underscore) between levels
- **Within a name:** single underscores are preserved (e.g. `client_id` stays `CLIENT_ID`)

> For the **complete, generated list** of every `TS2OTEL_*` variable with its default and
> description, see [`env-vars.md`](env-vars.md). The samples below just illustrate the rule.

### Mapping examples

| Config key | Environment variable |
|---|---|
| `tailscale.auth.oauth.client_id` | `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID` |
| `tailscale.auth.oauth.client_secret` | `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET` |
| `tailscale.auth.apikey` | `TS2OTEL_TAILSCALE__AUTH__APIKEY` |
| `otlp.endpoint` | `TS2OTEL_OTLP__ENDPOINT` |
| `otlp.grafana_cloud.token` | `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN` |
| `collectors.flowlogs.interval` | `TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL` |
| `collectors.flowlogs.source` | `TS2OTEL_COLLECTORS__FLOWLOGS__SOURCE` |
| `streaming.token` | `TS2OTEL_STREAMING__TOKEN` |
| `webhook.secret` | `TS2OTEL_WEBHOOK__SECRET` |
| `admin.auth.token` | `TS2OTEL_ADMIN__AUTH__TOKEN` |
| `prometheus.auth.token` | `TS2OTEL_PROMETHEUS__AUTH__TOKEN` |
| `self_observability.instance_id` | `TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID` |
| `profiling.pyroscope.basic_auth_password` | `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD` |

### Scalar lists

Fields whose type is a list of strings accept a **comma-separated value** as an env var. Examples:

```sh
TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES=all:read,log_streaming
TS2OTEL_COLLECTORS__NODE_METRICS__METRIC_ALLOW=tailscaled_inbound.*,tailscaled_outbound.*
TS2OTEL_COLLECTORS__NODE_METRICS__DROP_LABELS=job,prometheus_replica
TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_TAGS=tag:server,tag:relay
TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_NAMESPACES=intune,jamf,ip
```

### File-only fields

These fields cannot be set via flat env vars because they are maps or lists of structs:

- `otlp.headers` — use the YAML file (or use `otlp.grafana_cloud` for Grafana Cloud).
- `collectors.node_metrics.targets` — each target is a struct; static targets require a YAML file.
- `profiling.pyroscope.tags` — a string→string map; set via YAML.

### Unknown-variable advisory

A `TS2OTEL_*` env var that does not match any known config key is logged at startup as a **WARN** —
this almost always means a typo in the variable name. The exporter still starts; the variable is
ignored.

## Conventions

- **Default** is the value used when the key is not set in either the file or an env var.
- **Durations** use Go's syntax: `500ms`, `30s`, `5m`, `1h`, `168h` (= 7 days).
- **Validation** — invalid enum values and inconsistent combinations are rejected at startup by
  `Config.Validate()` (the exporter refuses to start). Softer issues are surfaced as startup
  **WARN** advisories by `Config.Warnings()` but do not block startup. Both are noted below.

## Contents

- [Top level](#top-level)
- [`tailscale` — API connection & authentication](#tailscale-api-connection-authentication)
- [`otlp` — the OTLP exporter](#otlp-the-otlp-exporter)
- [`enrichment` — device-name cache](#enrichment-device-name-cache)
- [`cardinality` — metric/label cardinality controls](#cardinality-metriclabel-cardinality-controls)
- [`collectors` — per-source polling](#collectors-per-source-polling)
- [`checkpoint` — poll high-water marks](#checkpoint-poll-high-water-marks)
- [`streaming` — Splunk-HEC log receiver](#streaming-splunk-hec-log-receiver)
- [`webhook` — event webhook receiver](#webhook-event-webhook-receiver)
- [`self_observability` — the exporter's own telemetry](#self_observability-the-exporters-own-telemetry)
- [`admin` — admin HTTP server (probes + status page)](#admin-admin-http-server-probes-status-page)
- [`prometheus` — Prometheus pull endpoint](#prometheus-prometheus-pull-endpoint)
- [`profiling` — pprof & Pyroscope](#profiling-pprof-pyroscope)
- [`tracing` — OTEL traces pillar](#tracing-otel-traces-pillar)

---

## Top level

| Key | Default | Description |
|-----|---------|-------------|
| `log_level` | `info` | Logging verbosity. One of `debug`, `info`, `warn`, `error`. |
| `provider` | `tailscale` | Control-plane backend. One of `tailscale` (default, fully back-compatible) or `headscale`. |

---

## `headscale` — Headscale control-plane connection

Used only when `provider: headscale`. Auth is a Bearer API key; keep it in an environment variable
(`TS2OTEL_HEADSCALE__API_KEY`), not in the YAML file.

Under `provider: headscale` only the `devices`, `users`, `keys`, `acl`, and `nodemetrics` collectors
run. The Tailscale-only collectors (`flowlogs`, `auditlogs`, `services`, `webhooks`, `contacts`,
`posture_integrations`, `log_stream`, `settings`, `dns`) auto-disable; enabling them explicitly triggers
a startup warning.

**Reduced device signal set.** Headscale's API exposes fewer device fields than Tailscale, so under
`provider: headscale` the `devices` collector emits a *subset* of its usual signals — online status,
advertised/enabled routes (exit-node and subnet-router derivations still work), key expiry, last-seen,
and tag/user counts. Tailscale-only signals have **no data** because the source fields are absent:
per-DERP-region latency, posture and posture attributes, tailnet-lock, update-available, OS/version
distribution, and connectivity quality. Likewise device share-invites and user-invites are unavailable.

**Headscale server metrics.** Headscale also exposes its own Prometheus endpoint (the *control-plane
server*, default `:9090`) — distinct from per-node `tailscaled` `:5252`. Scrape it by adding it as a
static `node_metrics` target (see the `node_metrics` section); there is no dedicated knob for it.

| Key | Default | Description |
|-----|---------|-------------|
| `headscale.url` | `""` | Headscale control-plane base URL, e.g. `https://headscale.example.org`. Required when `provider: headscale`. Set via `TS2OTEL_HEADSCALE__URL`. |
| `headscale.api_key` | `""` | Bearer API key for the Headscale server. Required when `provider: headscale`. Set via `TS2OTEL_HEADSCALE__API_KEY`. |
| `headscale.http.timeout` | `30s` | Per-request timeout for Headscale API calls. |
| `headscale.http.retry.max_attempts` | `0` | Accepted for config parity with `tailscale.http`, but **not applied** by the minimal v1 Headscale client (which honors only `timeout`). |
| `headscale.http.retry.base_delay` | `0s` | Accepted for parity; not applied in v1 (see above). |
| `headscale.http.retry.max_delay` | `0s` | Accepted for parity; not applied in v1 (see above). |
| `headscale.http.rate_limit` | `0` | Accepted for parity; not applied in v1 (see above). |

---

## `tailscale` — API connection & authentication

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.tailnet` | `-` | Your tailnet's name (e.g. `example.com`), or `-` (the default) for the authenticating principal's default tailnet — which works out of the box for a single-tailnet OAuth client. Set an explicit name only if the principal has access to multiple tailnets. |

### `tailscale.auth`

Prefer OAuth: its tokens are short-lived, auto-refreshing, and not bound to a user.

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.auth.method` | `oauth` | Authentication method. One of `oauth` (recommended) or `apikey`. |
| `tailscale.auth.oauth.client_id` | `""` | OAuth client ID. Required when `method: oauth`. Set via `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID`. |
| `tailscale.auth.oauth.client_secret` | `""` | OAuth client secret. Required when `method: oauth`. Set via `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET`. |
| `tailscale.auth.oauth.scopes` | `["all:read"]` | OAuth scopes requested for the token. Least-privilege read scopes are the default; add `log_streaming` if you use `streaming.auto_configure`. Comma-separated in env: `TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES=all:read,log_streaming`. |
| `tailscale.auth.apikey` | `""` | Personal API key. Used **only** when `method: apikey`. Set via `TS2OTEL_TAILSCALE__AUTH__APIKEY`. |

> **WARN (advisory):** `method: apikey` triggers a startup warning — a personal API key expires in
> ≤90 days and stops working when the user who created it is suspended or removed. For an unattended
> exporter, prefer `method: oauth`.

### `tailscale.http`

The HTTP client used for all Tailscale API calls.

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.http.timeout` | `30s` | Per-attempt timeout for each Tailscale API call (connect + headers + body read). Retries and `Retry-After` backoff are NOT counted against it, so a retried request can exceed this; total attempts are bounded by `max_attempts`. |
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
| `otlp.endpoint` | `https://otlp-gateway-prod-us-central-0.grafana.net/otlp` | OTLP endpoint (ignored when `protocol: stdout`). For `protocol: http` this is a full base **URL** — for Grafana Cloud use the `…/otlp` base and the per-signal `/v1/metrics` and `/v1/logs` paths are appended for you. For `protocol: grpc` it must instead be a bare **`host:port`** address (no scheme or path, e.g. `otlp-gateway-prod-us-central-0.grafana.net:443`); a URL-shaped value is rejected at startup. |
| `otlp.metric_interval` | `60s` | How often metrics are pushed. `60s` aligns with the default 1 data-point-per-minute scrape cadence and avoids Grafana Cloud DPM churn. |
| `otlp.headers` | `{}` | Extra raw headers added to every OTLP request (an alternative to `grafana_cloud`). |

### `otlp.grafana_cloud`

Convenience for Grafana Cloud: when both are set, an `Authorization: Basic <base64(instance:token)>`
header is built for you (no need to hand-craft it in `otlp.headers`).

| Key | Default | Description |
|-----|---------|-------------|
| `otlp.grafana_cloud.instance_id` | `""` | Grafana Cloud OTLP instance/stack ID (the Basic-auth username). Set via `TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID`. |
| `otlp.grafana_cloud.token` | `""` | Grafana Cloud OTLP token (the Basic-auth password). Set via `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN`. |

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

### `enrichment.reverse_dns`

Optional async reverse-DNS (PTR) enrichment of external (non-Tailscale) flow addresses. Off by
default. When enabled, resolved hostnames replace the `external` bucket / raw IP in
`tailscale.src.node` / `tailscale.dst.node` on flow logs and metrics. Lookups are async and cached;
the hot path never blocks.

| Key | Default | Description |
|-----|---------|-------------|
| `enrichment.reverse_dns.enabled` | `false` | Turn on reverse-DNS enrichment of external flow addresses. |
| `enrichment.reverse_dns.server` | `""` | Resolver to query as `ip` or `ip:port` (default port 53). Empty = system resolver. |
| `enrichment.reverse_dns.timeout` | `2s` | Per-lookup timeout. |
| `enrichment.reverse_dns.cache_ttl` | `1h` | Positive-result cache TTL. |
| `enrichment.reverse_dns.negative_ttl` | `5m` | Failed-lookup cache TTL. |
| `enrichment.reverse_dns.max_entries` | `4096` | Cache size bound. |
| `enrichment.reverse_dns.acknowledge_cardinality` | `false` | Set `true` (once `cardinality.metric_limit` is sized) to silence the startup advisory that fires when reverse-DNS is enabled together with node-dimension flow labels. |

---

## `cardinality` — metric/label cardinality controls

These knobs trade detail for active-series count. They apply to the shared processors, so they take
effect no matter whether logs arrive by poll or by stream.

### Top-level cardinality keys

| Key | Default | Description |
|-----|---------|-------------|
| `cardinality.metric_limit` | `10000` | Hard per-instrument series cap. Beyond this the OTLP SDK collapses extra series into `otel_metric_overflow` (silent loss of detail). Size it above your busiest flow-metric cardinality. `0` or negative = unlimited. |
| `cardinality.derp_region_rollup` | `true` | Emit tailnet-wide per-DERP-region rollup gauges (`tailscale.derp.region.*`) from the devices collector. |
| `cardinality.subnet_route_rollup` | `true` | Emit the per-CIDR `tailscale.subnet_routes.routers` redundancy gauge (one series per subnet CIDR) from the devices collector. The fleet exit/subnet count aggregates emit regardless. |

### `cardinality.flow` — flow metric shaping

These knobs affect flow **metrics** only. Flow **logs** always carry full detail regardless.

| Key | Default | Description |
|-----|---------|-------------|
| `cardinality.flow.metrics_mode` | `rollup` | Which flow metric families to emit. `rollup` — bounded top-N `*.rollup` families (lowest cardinality; adds per-source-node `tailscale.network.unique.*` gauges). `all` — per-connection raw families shaped by the toggles below. `both` — emit both (≈2× series; summing them double-counts — a startup WARN fires). |
| `cardinality.flow.rollup_top_n` | `500` | Number of busiest source/destination node pairs kept when `metrics_mode` is `rollup` or `both`; the rest fold into `__other__`. `0` selects the default (500). |
| `cardinality.flow.source_port` | `false` | Add `source.port` to flow **metrics** (raw families only). Ports are always present on flow **logs**. On = higher cardinality. |
| `cardinality.flow.destination_port` | `false` | Add `destination.port` to flow **metrics** (raw families only). On = higher cardinality. |
| `cardinality.flow.destination_service` | `false` | Add `tailscale.dst.service` (the IANA name for the destination port+transport, e.g. `tcp/443`→`https`) to flow **metrics** — a bounded, low-cardinality stand-in for the destination port. |
| `cardinality.flow.node_dims` | `true` | Include `tailscale.src.node`/`tailscale.dst.node` device names on flow metrics. |
| `cardinality.flow.collapse_external` | `true` | Bucket unresolved/off-tailnet IPs as `external`/`unknown` instead of the raw address. Off = one series per distinct external IP. |
| `cardinality.flow.exit_node_attribution` | `true` | Emit the bounded `tailscale.exit_node.io`/`tailscale.exit_node.packets` counters attributing exit traffic to the relaying node (bounded by exit-node count). Independent of `metrics_mode`. |

### `cardinality.per_entity` — per-entity gauge gates

When a toggle is `false`, only the low-cardinality aggregate `*.count` rollup is emitted; the
per-entity gauge series (one per device/user/key/…) are dropped. All default `true`.

| Key | Default | Description |
|-----|---------|-------------|
| `cardinality.per_entity.device` | `true` | Emit per-device gauges (online, last-seen, key-expiry, DERP latency, routes). `false` leaves only `tailscale.devices.count`. |
| `cardinality.per_entity.user` | `true` | Emit per-user gauges (devices, connected, last-seen). `false` leaves only `tailscale.users.count`. |
| `cardinality.per_entity.key` | `true` | Emit the per-key gauges (`tailscale.key.expiry`, `tailscale.key.scopes`, `tailscale.key.preauthorized`). `false` leaves only `tailscale.keys.count` (the "expiring soon" WARN log still fires). |
| `cardinality.per_entity.webhook` | `true` | Emit per-webhook gauges. `false` leaves only the aggregate count. |
| `cardinality.per_entity.service` | `true` | Emit per-service gauges. `false` leaves only the aggregate count. |

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
- **`stream`** — logs are **pushed** to the [`streaming`](#streaming-splunk-hec-log-receiver)
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
| `collectors.devices.collect_connectivity` | `true` | Emit per-device NAT/connectivity health (`tailscale.device.connectivity.*`: hard_nat, endpoints, direct_capable, udp, ipv6) plus the fleet connectivity rollups (`tailscale.devices.hard_nat`/`direct_capable`/`client_supports`). Read from the inline device data — **no extra API call**. Per-device gauges additionally gated by `cardinality.per_entity.device`. |
| `collectors.devices.collect_posture` | `false` | Also fetch device posture attributes (one **extra API call per device per tick**) and emit posture log events. |
| `collectors.devices.collect_device_invites` | `true` | Also fetch outstanding device share invites per device (one **extra API call per device per tick**, N+1) and emit `tailscale.device_invites.count`. Requires the `device_invites:read` OAuth scope (covered by `all:read`). Per-device failures are non-fatal. |
| `collectors.devices.posture_log_mode` | `changes` | Controls the `tailscale.device.posture` log (requires `collect_posture`). `changes` — full dump on first scrape then deltas only. `always` — every scrape. `off` — suppress the log (the posture gauge metric is still emitted). |
| `collectors.devices.attribute_namespaces` | `["intune","jamf","kandji","crowdstrike","sentinelone","kolide","ip"]` | Device posture-attribute namespace prefixes promoted to `tailscale.device.attribute{,.info}` metrics (requires `collect_posture`). `["*"]` promotes every namespace; `[]` disables the attribute metrics. Comma-separated in env: `TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_NAMESPACES=intune,jamf`. |
| `collectors.devices.collect_tag_rollup` | `true` | Emit the `tailscale.devices.by_tag` distribution gauge (one series per ACL tag). `false` keeps the other fleet-hygiene aggregates (`untagged`/`ephemeral`/`by_version`/`key_expiry`). |
| `collectors.devices.tag_rollup_limit` | `50` | Cap on distinct tag series for `tailscale.devices.by_tag`: the busiest N tags by device count keep their own series; the rest fold into a single `tailscale.tag="__other__"` series. `0` or negative = unlimited. |

### `collectors.flowlogs`

Network flow logs → aggregated traffic counters + per-connection flow logs.

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.flowlogs.enabled` | `true` | Whether flow logs are collected. |
| `collectors.flowlogs.source` | `poll` | `poll` \| `stream` \| `both`. See [`source`](#source-and-the-windowing-fields-flowlogs-auditlogs-only). |
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
| `collectors.auditlogs.source` | `poll` | `poll` \| `stream` \| `both`. See [`source`](#source-and-the-windowing-fields-flowlogs-auditlogs-only). |
| `collectors.auditlogs.interval` | `60s` | Poll cadence (poll only). |
| `collectors.auditlogs.lag` | `60s` | Tail-safety margin (poll only). |
| `collectors.auditlogs.initial_lookback` | `5m` | Cold-start reach-back (poll only). |
| `collectors.auditlogs.max_window` | `6h` | Catch-up cap for one tick (poll only). |

### Snapshot collectors

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.users.enabled` / `.interval` | `true` / `300s` | User/role/status counts and per-user device & connection gauges. |
| `collectors.keys.enabled` / `.interval` | `true` / `300s` | Key inventory gauges (auth keys, OAuth clients, and API tokens via the unified key model), counts bucketed by `type`/`auth_kind`/`revoked`/`invalid`, and an "expiring soon" WARN log. Per-key `key.expiry`/`key.scopes`/`key.preauthorized` gauges are gated by `cardinality.per_entity.key`. |
| `collectors.keys.expiry_warn` | `168h` | Emit the "expiring soon" WARN log when a key expires within this window (default 7 days). |
| `collectors.settings.enabled` / `.interval` | `true` / `600s` | Tailnet feature-toggle gauges. |
| `collectors.acl.enabled` / `.interval` | `true` / `600s` | ACL size + a "policy changed" signal (detected by ETag), plus policy risk-scoring gauges (wildcard / unrestricted / auto-approver / SSH-wildcard / posture-gated rules). |
| `collectors.dns.enabled` / `.interval` | `true` / `600s` | Nameserver / search-path / split-zone counts, the MagicDNS and override-local flags, the count of exit-node-eligible resolvers, and a per-resolver info gauge (`tailscale.dns.resolver`) labeled by address, kind, domain, and exit-node eligibility. |
| `collectors.contacts.enabled` / `.interval` | `true` / `600s` | Tailnet security-contact gauges. |
| `collectors.webhooks.enabled` / `.interval` | `true` / `600s` | Configured webhook gauges and per-webhook status. |
| `collectors.posture_integrations.enabled` / `.interval` | `true` / `600s` | MDM/EDR posture-integration gauges. |
| `collectors.log_stream.enabled` / `.interval` | `true` / `600s` | Log-streaming configuration gauges. |

### `collectors.services`

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.services.enabled` | `true` | Emit Tailscale VIP-Services gauges and counts. |
| `collectors.services.interval` | `600s` | Poll cadence. |
| `collectors.services.collect_hosts` | `false` | Also fetch per-service backing-host detail — one extra API call per service (N+1). Off by default. |

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
| `instance_source` | `name` | Identity-label source: `name` (MagicDNS short name — unique per tailnet **and** human-friendly; the default), `address` (Tailscale host:port — always unique), or `hostname` (OS hostname — **not** unique; collisions like `localhost` are auto-suffixed with the address + a WARN). |
| `include_host_labels` | `true` | Attach `host.name`/`host.id` for joins with `tailscale.device.*`. |
| `include_tags_label` | `true` | Attach `tailscale.tags`. |

---

## `checkpoint` — poll high-water marks

Checkpoints record how far each **polled** log collector (`flowlogs`/`auditlogs` with
`source: poll` or `both`) has read, so a restart resumes without gaps or large overlaps. The store is
**unused** if you stream both log types or disable them.

| Key | Default | Description |
|-----|---------|-------------|
| `checkpoint.store` | `file` | `file` \| `memory`. See below. |
| `checkpoint.file_path` | `/var/lib/tailscale2otel/checkpoints.json` | Where the file store persists. Used only when `store: file`. The parent directory is created automatically; if it cannot be made writable the exporter logs a WARN and falls back to `memory`. |

- **`file`** (default) — the high-water mark is persisted to `file_path` with an atomic write on each
  tick and reloaded at startup, so polling **resumes from the exact high-water mark** across restarts
  (minor boundary overlap is de-duplicated). For the checkpoint to actually survive a restart, mount a
  writable, **persistent** path at the file's directory (a volume in Kubernetes/Docker). If the path is
  not writable (e.g. a read-only root filesystem with no volume, or a local run without access to
  `/var/lib`), the exporter logs a WARN and transparently falls back to `memory` rather than erroring.
- **`memory`** — the high-water mark lives in RAM only and is lost on restart. After a restart the
  poller cold-starts from `initial_lookback`, so any **downtime longer than `initial_lookback` leaves
  a gap**. Needs no volume; fine for streamed or stateless deployments where the checkpoint is unused
  or disposable.

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
| `streaming.token` | `""` | Expected as `Authorization: Splunk <token>`. Set via `TS2OTEL_STREAMING__TOKEN`. |
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
| `webhook.secret` | `""` | Shared secret for HMAC-SHA256 verification. Set via `TS2OTEL_WEBHOOK__SECRET`. |
| `webhook.tolerance` | `5m` | Reject signed timestamps older than this (the replay window). `0` disables the timestamp check. |
| `webhook.dedup_audit_events` | `false` | Best-effort: drop a webhook event already counted via the audit logs (shares a cross-source de-dup set with the audit processor). |

---

## `self_observability` — the exporter's own telemetry

| Key | Default | Description |
|-----|---------|-------------|
| `self_observability.enabled` | `true` | Emit the exporter's own health metrics (`tailscale2otel.*`: scrape duration/success/errors, API requests/retries, cardinality, …). |
| `self_observability.instance_id` | `""` | Sets the `service.instance.id` resource attribute so multiple exporter instances are distinguishable. Empty falls back to the host name. In Kubernetes set via env: `TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID=$POD_NAME`. |

---

## `pii_filter` — PII / identifier redaction

Runtime opt-out toggles for each identifier category. All 13 categories default to **`true`**
(identifiers are emitted as-is). Set a category to `false` to drop those identifiers from metrics
and logs at collection time. Gauges whose only meaningful identity is a redacted category are
suppressed entirely. Categories are independent — you can redact external IPs while keeping
Tailscale IPs, for example.

| Key | Default | Description |
|-----|---------|-------------|
| `pii_filter.emails` | `true` | User/actor login names (frequently email addresses, e.g. `enduser.login`). |
| `pii_filter.user_display_names` | `true` | Actor display (human) names (e.g. `enduser.name`). |
| `pii_filter.user_ids` | `true` | Numeric/opaque user IDs (e.g. `enduser.id`). |
| `pii_filter.hostnames` | `true` | Device and collector-host hostnames. |
| `pii_filter.node_ids` | `true` | Tailscale node IDs (e.g. the `nodeId` field on a device). |
| `pii_filter.tailscale_ips` | `true` | Tailscale overlay addresses: `100.64.0.0/10` (IPv4) and `fd7a:115c:a1e0::/48` (IPv6). |
| `pii_filter.internal_ips` | `true` | RFC 1918 / ULA / link-local addresses (non-Tailscale private ranges). |
| `pii_filter.external_ips` | `true` | Public/routable (non-private) IP addresses. |
| `pii_filter.service_addrs` | `true` | VIP service names from the Tailscale Services collector. |
| `pii_filter.endpoint_paths` | `true` | Tailscale API endpoint paths carried on self-observability spans and metrics. |
| `pii_filter.network_topology` | `true` | Route CIDRs, split-DNS domains, and search paths from the DNS/ACL collectors. |
| `pii_filter.tailnet_name` | `true` | The tailnet identifier (e.g. `example.com` or the numeric tailnet ID). Disabling it also omits the universal `tailscale.tailnet` attribute from every metric, log, and span; in multi-tailnet mode that removes the per-tailnet label (series stay distinct via `service.instance.id`). |
| `pii_filter.free_text_details` | `true` | Audit `old`/`new`/`details` payloads, target names, key descriptions, and posture values. |

> **Note:** these toggles gate emission only — they do not encrypt or hash values. Setting a
> category to `false` simply omits that class of identifier from emitted telemetry entirely.

> **Limitation — log message bodies are NOT redacted.** The filter applies to metric labels and
> log record **attributes** only. Log record **bodies** are fixed human-readable strings that do
> not contain PII identifiers (hostnames, emails, IPs, etc.) by design — they use only non-PII
> fields such as action type, target type, and counts. However, two signal bodies carry
> free-text detail that is controlled by `pii_filter.free_text_details`:
>
> - **`tailscale.acl.risky_rule`** — when `free_text_details` is **enabled** (`true`), the body
>   contains the offending ACL rule text (e.g. `src=tag:servers/dst=*:*`); the matching
>   `tailscale.audit.details` **attribute** is redacted when `free_text_details` is `false`, but
>   the body is unchanged.
> - **`tailscale.key.scopes`** — when `free_text_details` is **enabled** (`true`), the body
>   contains the key description text; the matching attribute is redacted when `false`, but the
>   body is unchanged.
>
> Operators with strict PII posture should also restrict log-body retention at the backend
> (e.g. via Grafana Cloud log pipeline drop/mask rules) if these bodies present a risk.

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
| `admin.auth.token` | `""` | When set, the status page and pprof require this token as the HTTP Basic password (browsers prompt) **or** `Authorization: Bearer <token>`. `/healthz` and `/readyz` are never gated. Set via `TS2OTEL_ADMIN__AUTH__TOKEN`. |

> **WARN (advisory):** if `landing_page` is served on a wildcard (all-interfaces) bind with no
> `admin.auth.token`, a startup warning fires — the page exposes internal state to anyone who can
> reach the port. Set a token or bind to loopback/tailnet.

---

## `prometheus` — Prometheus pull endpoint

An opt-in, off-by-default `GET /metrics` endpoint on a **dedicated listener** (`prometheus.listen`,
default `:2112`). When enabled it attaches an additional `metric.Reader` (a per-provider Prometheus
registry) alongside the OTLP push path, so **both export paths are active at once** — Prometheus
scraping and OTLP push are independent and complementary; enabling one does not disable the other.

The endpoint is fully separate from the admin server (`admin.listen`) and must bind to a different
address. It serves only `GET /metrics`; no status page or probes are exposed here.

> **Multi-tailnet:** each tailnet's metrics carry a `tailscale_tailnet="<name>"` **constant label**
> on every Prometheus series so multi-tailnet series do not collide at a shared `/metrics` endpoint.
> A `target_info` info metric is also emitted per provider. On **Grafana Cloud** the primary metrics
> path is OTLP (which uses the `target_info` join for resource attributes); the Prometheus endpoint
> is an additional pull-compatible path for existing Prometheus-only infrastructure. See roadmap item
> L for the planned native per-tailnet metric-attribute promotion.

| Key | Default | Description |
|-----|---------|-------------|
| `prometheus.enabled` | `false` | Run the Prometheus pull endpoint on its own dedicated listener. Off by default. |
| `prometheus.listen` | `:2112` | Listen address for `/metrics`. Must differ from `admin.listen`. For defense-in-depth bind to loopback (`127.0.0.1:2112`) or a tailnet IP rather than a wildcard. |
| `prometheus.auth.token` | `""` | Optional shared secret gating `/metrics`. Accepted as the HTTP Basic password (any username) **or** `Authorization: Bearer <token>`. Empty = open (unauthenticated). Set via `TS2OTEL_PROMETHEUS__AUTH__TOKEN`. |

> **WARN (advisory):** if `prometheus.enabled` is `true` on a wildcard bind (empty host, e.g.
> `:2112`) with no `prometheus.auth.token`, a startup warning fires — the endpoint exposes every
> series (including device hostnames, flow identifiers, and tailnet name) to anyone who can reach
> the port. Set a token or bind to loopback/tailnet.

> **Validation:** `prometheus.listen` and `admin.listen` must differ when both servers are enabled
> (the exporter errors at startup if they share the same address).

### Prometheus `scrape_configs` snippet

```yaml
scrape_configs:
  - job_name: tailscale2otel
    static_configs:
      - targets: ["host:2112"]
    # If prometheus.auth.token is set:
    authorization:
      credentials: "<token>"
```

---

## `profiling` — pprof & Pyroscope

Optional continuous/on-demand profiling. Everything here is **off by default** and carries no
Tailscale data. The pprof handlers mount on the admin server.

| Key | Default | Description |
|-----|---------|-------------|
| `profiling.pprof.enabled` | `false` | Mount `net/http/pprof` handlers on the admin server so Alloy's `pyroscope.scrape` (or `go tool pprof`) can pull profiles. |
| `profiling.pyroscope.enabled` | `false` | Run the Pyroscope continuous-profiling push agent. |
| `profiling.pyroscope.server_address` | `""` | Pyroscope/Grafana Cloud Profiles URL. **Required when `pyroscope.enabled`.** |
| `profiling.pyroscope.basic_auth_user` | `""` | Grafana Cloud: the profiles instance ID (Basic-auth user). Set via `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER`. |
| `profiling.pyroscope.basic_auth_password` | `""` | Grafana Cloud: an access-policy token with `profiles:write` (Basic-auth password). Set via `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD`. |
| `profiling.pyroscope.tenant_id` | `""` | `X-Scope-OrgID` for multi-tenant servers (leave empty for Grafana Cloud). |
| `profiling.pyroscope.upload_rate` | `60s` | How often profiles are flushed to the server. |
| `profiling.pyroscope.tags` | `{}` | Extra static labels merged onto every profile, e.g. `{ env: prod }`. Must be set via YAML (map field). |
| `profiling.mutex_profile_fraction` | `0` | `runtime.SetMutexProfileFraction` (`0` = disabled). Mutex profiles stay empty until set. |
| `profiling.block_profile_rate` | `0` | `runtime.SetBlockProfileRate` (`0` = disabled). Block profiles stay empty until set. |

> **Validation / advisories:**
> - `pprof.enabled` errors at startup unless `admin.enabled: true` **and** `admin.auth.token` is set
>   (heap/goroutine dumps can expose in-memory secrets, so pprof must not be served unauthenticated).
> - `pyroscope.enabled` errors at startup without `pyroscope.server_address`.
> - A `grafana.net` `server_address` with an empty `basic_auth_password` triggers a **WARN** —
>   Grafana Cloud Profiles requires the Basic-auth credentials.

---

## `version_checks` — outbound "is a newer release available?" checks

Optional outbound checks that compare the running build / device client versions against the latest
releases. Both sub-checks make external HTTPS calls and are **fail-open** (a failed or blocked fetch
silently emits nothing, never errors). Disable both for air-gapped deployments.

| Key | Default | Description |
|-----|---------|-------------|
| `version_checks.self.enabled` | `true` | Emit `tailscale2otel.update_available` (0/1 flag) comparing the running build to the latest tailscale2otel GitHub release. Independent of `self_observability.enabled`. |
| `version_checks.devices.enabled` | `true` | Emit per-device `tailscale.device.version_skew` (minor releases behind latest Tailscale stable), `tailscale.fleet.latest_version` (info gauge), and `tailscale.devices.outdated` (fleet count). Requires the `devices` collector; a WARN fires if the collector is disabled. |
| `version_checks.devices.outdated_minor_threshold` | `3` | A device at least this many minor releases behind the latest Tailscale stable counts toward `tailscale.devices.outdated`. Must be ≥ 1. |
| `version_checks.cache_ttl` | `1h` | How long a fetched "latest version" is cached before re-fetching. Must be ≥ 5m (validated). |
| `version_checks.timeout` | `10s` | Per-request timeout for the external version fetch. Must be > 0. |

> **Advisories:**
> - `version_checks.devices.enabled=true` with `collectors.devices.enabled=false` triggers a **WARN** —
>   the per-device version-skew metrics need the devices collector to run.

---

## `tracing` — OTEL traces pillar

Optional OTEL traces pillar. **Off by default.** When enabled, the exporter emits spans for its own
internal work — reusing `otlp.*` for the endpoint/protocol/headers/TLS (no separate trace endpoint).
When `tracing.enabled` is true, the metric exemplar filter also flips to trace-based, so the
`tailscale2otel.api.duration` latency histogram carries trace exemplars that link directly to the
corresponding API request span.

| Key | Default | Description |
|-----|---------|-------------|
| `tracing.enabled` | `false` | Emit spans. When true, also enables trace-based exemplars on `tailscale2otel.api.duration`. Set via `TS2OTEL_TRACING__ENABLED`. |
| `tracing.sampler` | `parentbased_always_on` | Head sampler. One of `always_on`, `always_off`, `traceidratio`, `parentbased_always_on`, `parentbased_traceidratio`. Mirrors `OTEL_TRACES_SAMPLER` semantics. Set via `TS2OTEL_TRACING__SAMPLER`. |
| `tracing.sampler_arg` | `1.0` | Sample ratio in `[0,1]` for the `*traceidratio` samplers; ignored by the others. Set via `TS2OTEL_TRACING__SAMPLER_ARG`. |

> **Advisories:**
> - `tracing.enabled=true` with `sampler_arg=0` and a `*traceidratio` sampler triggers a **WARN** —
>   no spans will be recorded at ratio 0.

### Span names and key attributes

When `tracing.enabled` is true the following spans are emitted:

| Span name | Emitted by | Key attributes |
|---|---|---|
| `scrape <collector>` | Scheduler (one per scrape cycle) | `tailscale.collector` (collector name); span status `Error` on failure |
| `tailscale.api <endpoint>` | Tailscale API transport (one per logical request) | `url.full` (full path incl. tailnet/device ID — useful for "which device's request was slow/failed"), `http.request.method`, `http.response.status_code`, `http.request.resend_count`, `server.address`; retry events carry `attempt`/`status`/`sleep_ms` |
| `stream.receive` | HEC stream receiver (one per HTTP request) | `tailscale.stream.flows`, `tailscale.stream.audits`, `tailscale.stream.skipped`, `http.request.body.size`; span status `Error` on auth/parse failure |
| `webhook.receive` | Webhook receiver (one per HTTP request) | `tailscale.webhook.events`, `http.request.body.size`; span status `Error` on auth/parse failure |

**PII note:** Spans are unaggregated (like logs), so useful identifiers such as the tailnet name and device ID
appear on `url.full` by design — they help operators answer "which device's request failed or was slow?"
Tier-1 secrets (auth headers/tokens, OAuth/webhook/logstream credentials) and large response/request
bodies are never attached. Per-record source/destination IPs are not put on receiver spans; they flow to
the flow/audit log records instead.
