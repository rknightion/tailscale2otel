# tailscale2otel

Poll the [Tailscale API](https://tailscale.com/api) for every available kind of observability
data and export it as **OpenTelemetry-native metrics and logs** — optimized for Grafana Cloud
(OTLP) but compatible with any OTEL backend.

Tailscale exposes a rich observability surface (network flow logs, configuration audit logs, a
detailed device inventory, users, keys, settings, ACL, DNS) but no Prometheus endpoint, and it
streams logs only to SIEM/storage sinks. `tailscale2otel` synthesizes well-modeled,
[semantic-convention](https://opentelemetry.io/docs/specs/semconv/)-compliant OTEL telemetry from
that data so you get device-fleet health, network throughput by node/protocol, an audit/event
stream, and key-expiry signals out of the box.

## Features

- **Network flow logs → metrics + logs.** Aggregated `tailscale.network.io`/`.packets`/`.flows`
  counters (low cardinality) for dashboards & alerting, plus full-fidelity per-connection flow
  records as OTEL logs for drill-down. Source IPs are enriched to device names.
- **Configuration audit logs → logs + counter.**
- **Device inventory, users, keys, settings, ACL, DNS** → gauges (online status, key expiry,
  per-user device counts, feature toggles, …).
- **Two ingestion paths for logs (pick one):** poll the API, or receive Tailscale's **log
  streaming** via a built-in Splunk-HEC-compatible receiver — both feed the same conversion
  pipeline. Choose one method per log type; running both is a discouraged fallback guarded by a
  best-effort de-dup failsafe (see below).
- **Optional webhook receiver** for real-time Tailscale events (HMAC-verified).
- **Optional node-metrics scraper** that forwards `tailscaled` per-node Prometheus `/metrics`
  centrally over OTLP (counters as deltas, gauges as gauges), as a drop-in for per-node scraping.
- **OTLP push** (gRPC/HTTP) with first-class Grafana Cloud support; `stdout` mode for local debug.
- **Admin status page** at `/` (plus `/healthz`/`/readyz` and a `/api/status.json`) showing live
  collector health, active-series cardinality, the metrics/log catalog, discovered nodes, and a
  redacted config — and **opt-in continuous profiling** (pprof for Alloy, or Pyroscope push).
- Lightweight single static binary, pluggable per-source polling with jitter, failure isolation,
  checkpointing, an in-memory device-enrichment cache, and self-observability metrics.

## Quick start

### Docker

```sh
docker build -f deploy/Dockerfile -t tailscale2otel .
docker run --rm \
  -e TS_TAILNET=example.com \
  -e TS_OAUTH_CLIENT_ID=... -e TS_OAUTH_CLIENT_SECRET=... \
  -e GC_INSTANCE_ID=... -e GC_OTLP_TOKEN=... \
  tailscale2otel
```

### Binary

```sh
go build -o tailscale2otel ./cmd/tailscale2otel
cp config.example.yaml config.yaml   # then edit / set env vars
./tailscale2otel -config config.yaml
```

### Local debug (no backend)

Set `otlp.protocol: stdout` to print metrics & logs to the console.

## Configuration

Copy [`config.example.yaml`](./config.example.yaml) and edit it (or just set the `${ENV}` vars it
references — every string value supports `${ENV}` expansion, so keep secrets in environment
variables). That commented example is the fastest way in; for an exhaustive, per-key reference of
**every** setting, default, and gotcha, see **[`docs/configuration.md`](./docs/configuration.md)**.

**Authentication** — prefer an [OAuth client](https://tailscale.com/kb/1215/oauth-clients)
(`method: oauth`, no fixed expiry, auto-refreshing) with least-privilege read scopes; an API key
(`method: apikey`) also works.

**Grafana Cloud** — set `otlp.protocol: http`, point `otlp.endpoint` at your
`https://otlp-gateway-<region>.grafana.net/otlp`, and fill `otlp.grafana_cloud.{instance_id,token}`;
the Basic-auth header is built for you. For a self-hosted Collector/Alloy, use `protocol: grpc` or
`http` with your own endpoint/headers.

### Log collectors: poll vs. stream

The two log collectors — `flowlogs` and `auditlogs` — can obtain data two ways, chosen per
collector with `source`:

- **`source: poll`** (default) — `tailscale2otel` pulls the logs from the Tailscale API on a
  schedule, one time-window per tick. Four windowing fields tune that polling:
  - `interval` — how often a window is polled.
  - `lag` — only query up to `now − lag`, so records still arriving at the tail aren't missed.
  - `initial_lookback` — how far back a cold start reaches when there is no checkpoint yet.
  - `max_window` — caps a single tick's window so a long outage is caught up over several ticks
    instead of one huge request.
- **`source: stream`** — the logs are *pushed* to the built-in Splunk-HEC receiver instead (see
  [Log streaming (HEC) & webhooks](#log-streaming-hec--webhooks) below); `tailscale2otel` does not
  poll. **The four windowing fields above (`interval`, `lag`, `initial_lookback`, `max_window`) have
  no effect in this mode** — only `enabled`, `source`, and (for `flowlogs`) the output-shaping
  `log_mode` / `max_log_records_per_window` apply.
- **`source: both`** — poll *and* accept the stream. Discouraged: the same record can be
  double-counted (cross-source de-dup is only a best-effort failsafe), and a startup WARN fires.

Pick exactly one method per log type. Output shaping — `flowlogs.log_mode` and the `cardinality.*`
knobs — applies regardless of which path delivers the records.

```yaml
# Poll (default): tailscale2otel pulls logs on a schedule.
flowlogs:  { enabled: true, source: poll, interval: 60s, lag: 120s, initial_lookback: 5m, max_window: 1h }

# Stream: Tailscale pushes logs to the HEC receiver; the window fields are omitted (they'd be ignored).
flowlogs:  { enabled: true, source: stream, log_mode: per_connection }
```

### Checkpointing

Checkpoints record how far each *polled* log collector has read, so a restart resumes without gaps
or large overlaps. They matter **only** when `flowlogs`/`auditlogs` use `source: poll` (or `both`);
if you stream both log types — or disable them — the checkpoint store is unused.

- **`checkpoint.store: memory`** (default) — held in RAM only. Simplest, needs no volume, but on
  restart the poller cold-starts from `initial_lookback`, so any downtime longer than that leaves a
  gap. Fine for streamed or stateless deployments.
- **`checkpoint.store: file`** — persisted to `checkpoint.file_path` (atomic write each tick) and
  reloaded at startup, so polling resumes from the exact high-water mark across restarts (minor
  overlap is de-duplicated). Use this when you poll logs and want continuity; it needs a writable,
  **persistent** path such as a mounted volume. An empty `file_path` silently falls back to memory.

## Collectors

| Collector | Cadence (default) | Emits |
|-----------|-------------------|-------|
| `devices` | 60s | device online/last-seen/key-expiry/update gauges, fleet counts; **feeds the enrichment cache** |
| `flowlogs` | 60s (lag 120s) | aggregated traffic counters + per-connection flow logs |
| `auditlogs` | 60s (lag 60s) | audit-event logs + a counter |
| `users` | 300s | user/role/status counts, per-user device & connection gauges |
| `keys` | 300s | key-expiry gauges, counts, and an "expiring soon" warning log |
| `settings` | 600s | tailnet feature-toggle gauges |
| `acl` | 600s | ACL size + "policy changed" signal (by ETag) |
| `dns` | 600s | nameserver / search-path / split-zone counts, MagicDNS flag |
| `node_metrics` | 60s | **(opt-in)** scrapes configured `tailscaled` `/metrics` endpoints, forwarding counters as deltas and gauges with an `instance` label + a per-target `tailscale.node.up` |

Each collector can be disabled or re-tuned in config. `flowlogs`/`auditlogs` take a `source` of
`poll`, `stream`, or `both` — **pick one method per log type** (`poll` *or* `stream`). `both` (and
enabling `streaming` while a collector still polls) risks double-counting; cross-source de-dup is a
best-effort failsafe and the exporter WARNs at startup when it sees this. `node_metrics` is off by
default and disabled when no targets are set.

## Dashboards & metrics reference

- Ready-to-import Grafana 13 dashboards live in [`deploy/grafana/`](./deploy/grafana/) — device
  **fleet & inventory**, network **flow & throughput**, **audit & webhook events** (logs), and
  **exporter health**. They use `${DS_PROM}`/`${DS_LOKI}` datasource variables, so pick your
  Prometheus/Loki datasources on import. See [`deploy/grafana/README.md`](./deploy/grafana/README.md).
- A full catalog of every metric and log event — including the OTLP→Prometheus name normalization
  (e.g. `tailscale.network.io` → `tailscale_network_io_bytes_total`, unit-`1` gauges → `*_ratio`) —
  is in [`docs/metrics.md`](./docs/metrics.md).

## Log streaming (HEC) & webhooks

Set a log collector's `source: stream` and enable the `streaming` receiver to have Tailscale push
logs to this service as a Splunk-HEC sink (ideally over a private endpoint inside your tailnet,
using a `tailscale cert` for HTTPS). When you do this, set `source: stream` (not `poll`/`both`) so
each log type is ingested by exactly one path — running the poller and the receiver for the same
log type risks double-counting, and cross-source de-dup is only a best-effort failsafe (the exporter
WARNs at startup if both are active). Enable the `webhook` receiver to ingest real-time Tailscale
events. All receivers are off by default.

Set `streaming.auto_configure: true` (with `streaming.enabled: true`, a `streaming.public_url`, and
an OAuth client carrying the `log_streaming` scope) to have the service register itself as the
Splunk-HEC sink on startup instead of configuring the stream by hand. It is off by default.

> Note: Tailscale does not publicly document the exact HEC payload envelope; the receiver parses
> defensively and the envelope should be confirmed by capturing a live stream in your environment.

## Admin status page & profiling

Enable the admin server (`admin.enabled: true`) and it serves liveness/readiness probes at
`/healthz` and `/readyz`. Unless you set `admin.landing_page: false`, it also serves a
Prometheus-exporter-style **status page** at `/` and the same snapshot as JSON at `/api/status.json`.
The page surfaces, live and in-process:

- per-collector health (last run, success/failure, last error, interval, run/failure counts);
- **active-series cardinality** for the last export interval (when `self_observability.enabled`);
- the full **metrics & log-event catalog** with OTLP→Prometheus names and attributes;
- **discovered node-metrics targets** (when dynamic discovery is on) — a collapsible list;
- the device-enrichment cache, dedup-set occupancy, Go runtime stats, and a **redacted** config
  summary (secret *values* never appear — only which secrets are set, and OTLP header key names).

For defense-in-depth, bind `admin.listen` to a tailnet or loopback address so only the tailnet can
reach it. Set `admin.auth.token` (keep it in `${ADMIN_TOKEN}`) to require a shared secret on the
status page and pprof — present it as the HTTP Basic password (browsers prompt) or as
`Authorization: Bearer <token>`. `/healthz` and `/readyz` are **never** gated, so health checks keep
working. With no token the status page stays open (a startup WARN fires if it's exposed on an
all-interfaces bind); rejected requests increment `tailscale2otel.admin.auth.rejected`.

**Continuous profiling** is opt-in (`profiling.*`, all off by default):

- `profiling.pprof.enabled: true` mounts the standard `/debug/pprof/*` handlers on the admin server
  so Grafana Alloy's `pyroscope.scrape` (or `go tool pprof`) can **pull** profiles. Requires
  `admin.enabled: true` **and** `admin.auth.token` (heap/goroutine dumps can expose in-memory
  secrets, so pprof must not be served unauthenticated).
- `profiling.pyroscope.enabled: true` **pushes** profiles to Pyroscope / Grafana Cloud Profiles via
  the [pyroscope-go](https://github.com/grafana/pyroscope-go) SDK (`server_address` required; basic
  auth via `PYROSCOPE_BASIC_AUTH_USER`/`PYROSCOPE_BASIC_AUTH_PASSWORD`).
- Mutex/block profiles are off until `profiling.mutex_profile_fraction` / `profiling.block_profile_rate`
  are set above zero (they apply to both the push and pull paths).

## Development

```sh
go test -race ./...      # unit + integration tests
go vet ./...
```

The codebase is organized as small, single-purpose packages under `internal/`: `telemetry`
(OTEL facade + providers), `collector` (scheduler/registry/checkpoints + one package per source),
`tsapi` (Tailscale client + log doers), `flowlog`/`audit` (record types + shared processors),
`enrich` (device cache), `config`, and the `stream`/`webhook` receivers.

## License

TBD.
