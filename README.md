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

See [`config.example.yaml`](./config.example.yaml). All string values support `${ENV}` expansion,
so keep secrets in environment variables.

**Authentication** — prefer an [OAuth client](https://tailscale.com/kb/1215/oauth-clients)
(`method: oauth`, no fixed expiry, auto-refreshing) with least-privilege read scopes; an API key
(`method: apikey`) also works.

**Grafana Cloud** — set `otlp.protocol: http`, point `otlp.endpoint` at your
`https://otlp-gateway-<region>.grafana.net/otlp`, and fill `otlp.grafana_cloud.{instance_id,token}`;
the Basic-auth header is built for you. For a self-hosted Collector/Alloy, use `protocol: grpc` or
`http` with your own endpoint/headers.

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
