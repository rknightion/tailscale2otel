# tailscale2otel

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/rknightion/tailscale2otel/badge)](https://scorecard.dev/viewer/?uri=github.com/rknightion/tailscale2otel)

Poll the [Tailscale API](https://tailscale.com/api) for every available kind of observability
data and export it as **OpenTelemetry-native metrics and logs** (plus an optional **traces** pillar
for the exporter's own self-observability) — optimized for Grafana Cloud (OTLP) but compatible with
any OTEL backend.

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
- **Configuration audit logs → logs + counters,** including a curated security-/lifecycle-categorized
  change counter (`tailscale.config.audit.changes`) for low-noise alerting.
- **Device inventory, users, keys, settings, ACL, DNS** → gauges (online status, connectivity/NAT
  quality, exit-node & subnet-router analytics, fleet hygiene roll-ups, key expiry, per-user device
  counts, feature toggles, …). Key inventory spans auth keys, OAuth clients, and API tokens; ACL
  policy is scored for structural risk (wildcard / unrestricted / auto-approver / SSH / posture).
- **Self update-available + device version-skew** signals from the GitHub/Tailscale release feeds
  (both opt-out for air-gapped deployments).
- **Optional OTEL traces pillar** (`tracing.enabled`, off by default) — spans per scrape cycle,
  Tailscale API request, and receiver request, with exemplars linking `tailscale2otel.api.duration`
  to the originating API span. Reuses the `otlp.*` endpoint.
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

### Docker (env-only — no file to mount)

The config file is optional. Pass a handful of `TS2OTEL_*` environment variables and the exporter
starts from built-in defaults plus those overrides — nothing to mount:

```sh
docker build -f deploy/Dockerfile -t tailscale2otel .
docker run --rm \
  -e TS2OTEL_TAILSCALE__TAILNET=example.com \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id> \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token> \
  tailscale2otel
```

### Docker (with a config file)

If you prefer a YAML file for the non-secret fields, mount it and pass `-config`:

```sh
docker run --rm \
  -v "$PWD/config.yaml:/etc/tailscale2otel/config.yaml:ro" \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token> \
  tailscale2otel -config /etc/tailscale2otel/config.yaml
```

### Binary

```sh
go build -o tailscale2otel ./cmd/tailscale2otel
cp config.example.yaml config.yaml   # then edit; secrets stay in env vars
./tailscale2otel -config config.yaml
```

### Local debug (no backend)

Set `TS2OTEL_OTLP__PROTOCOL=stdout` (or `otlp.protocol: stdout` in YAML) to print metrics & logs to
the console.

## Configuration

Configuration is **layered** — lowest precedence first:

1. **Built-in defaults** — the exporter runs without a config file at all.
2. **YAML file** (optional) — pass `-config path/to/config.yaml`; the file overrides defaults for
   any keys it mentions.
3. **Environment variables** — highest precedence; override both defaults and the file.

Every config field is settable via an env var named `TS2OTEL_` + the dotted key path, with `__`
(double underscore) between nesting levels and single underscores within a name preserved:

| Config key | Environment variable |
|---|---|
| `tailscale.auth.oauth.client_secret` | `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET` |
| `otlp.endpoint` | `TS2OTEL_OTLP__ENDPOINT` |
| `collectors.flowlogs.interval` | `TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL` |
| `self_observability.instance_id` | `TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID` |

The **complete list** of every `TS2OTEL_*` variable, with defaults and descriptions, lives in
[`docs/env-vars.md`](./docs/env-vars.md) (generated from `config.example.yaml`).

Keep secrets (tokens, client secrets) in env vars — they never need to appear in YAML. Scalar list
fields (e.g. `tailscale.auth.oauth.scopes`) accept a comma-separated env value. Map fields
(`otlp.headers`) and the `node_metrics.targets` list-of-structs must be set via a config file.

A `TS2OTEL_*` env var that does not match any known config key is logged at startup as a **WARN** —
this usually indicates a typo in the variable name.

[`config.example.yaml`](./config.example.yaml) shows the common knobs with comments; for an
exhaustive, per-key reference of **every** setting, default, and gotcha, see
**[`docs/configuration.md`](./docs/configuration.md)**.

**Authentication** — prefer an [OAuth client](https://tailscale.com/kb/1215/oauth-clients)
(`method: oauth`, no fixed expiry, auto-refreshing) with least-privilege read scopes; an API key
(`method: apikey`) also works. Set credentials via env vars:

```sh
TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<id>
TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<secret>
```

**Grafana Cloud** — set `otlp.protocol: http`, point `otlp.endpoint` at your
`https://otlp-gateway-<region>.grafana.net/otlp`, and fill `otlp.grafana_cloud.{instance_id,token}`;
the Basic-auth header is built for you. Set the token via env var:

```sh
TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id>
TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token>
```

For a self-hosted Collector/Alloy, use `protocol: grpc` or `http` with your own endpoint/headers.

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

- **`checkpoint.store: file`** (default) — persisted to `checkpoint.file_path` (atomic write each
  tick) and reloaded at startup, so polling resumes from the exact high-water mark across restarts
  (minor overlap is de-duplicated). Mount a writable, **persistent** path at the file's directory (a
  volume in Docker/Kubernetes) so it survives restarts. If the path isn't writable, the exporter logs
  a WARN and falls back to memory rather than erroring — so it's safe everywhere.
- **`checkpoint.store: memory`** — held in RAM only. Needs no volume, but on restart the poller
  cold-starts from `initial_lookback`, so any downtime longer than that leaves a gap. Fine for
  streamed or stateless deployments.

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

> Security: an empty `webhook.secret` skips HMAC verification and an empty `streaming.token` disables
> receiver auth. Set these via `TS2OTEL_WEBHOOK__SECRET` and `TS2OTEL_STREAMING__TOKEN` — an env var
> that is not set resolves to empty and silently disables auth, so double-check the variable names
> (a typo is logged at startup as a WARN). `streaming.auto_configure` overwrites the tailnet's
> existing log-streaming sink. The exported flow/audit telemetry also carries IPs, ports, device
> names, and user identities. See [`SECURITY.md`](SECURITY.md) for the full data-handling and
> receiver-auth notes.

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
reach it. Set `admin.auth.token` (via `TS2OTEL_ADMIN__AUTH__TOKEN`) to require a shared secret on
the status page and pprof — present it as the HTTP Basic password (browsers prompt) or as
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
  auth via `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER` /
  `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD`).
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

## API drift CI

Tailscale's API and its OpenAPI spec evolve continuously ("may change or break without notice"),
which has broken our decoders before. Four lanes guard against it:

| Lane | When | What it checks |
| --- | --- | --- |
| **Schema-driven decode fuzz** | every PR (`go test ./...`) | synthesizes payloads from the vendored OpenAPI spec + hand-written wire quirks (numeric `proto`, polymorphic audit `old`/`new`, `date-time`) and runs them through the real `tsapi` decoders |
| **OpenAPI drift** (`api-drift.yml`) | weekly + on demand | diffs the live spec (`?outputOpenapiSchema=true`) against the vendored copy, scoped to the operations we actually consume, and classifies breaking/info changes |
| **Client-lib tracking** (`clientlib-main.yml`) | weekly + on demand | builds + tests against `tailscale-client-go/v2@main` and `@latest` (ephemeral; never committed) |
| **Live contract** (`live-contract.yml`) | weekly + on demand | hits the real API read-only and asserts every consumed GET still decodes |

The consumed surface lives in `internal/tsapi/contract` (manifest + decoder harness); the spec parser
and drift classifier are in `internal/oas`; `tools/apidrift` is the drift CLI. When a scheduled lane
detects a problem it opens/updates a deduplicated tracking issue, optionally enriches it with a Claude
fix (and draft PR), and fails the run. Scheduled lanes are advisory — only the PR-time fuzz lane gates PRs.

### One-time setup

```sh
gh label create api-drift -c FBCA04
gh label create clientlib-drift -c FBCA04
gh label create live-contract -c FBCA04
```

The live lane stores **no Tailscale key in GitHub**. It runs on a **dedicated self-hosted runner**
(label `tailscale-api`, distinct from the shared pool) and mints a short-lived token from Tailscale's
OAuth endpoint (`POST /api/v2/oauth/token`, `grant_type=client_credentials`) using a **read-only
(`all:read`) OAuth client** whose `TS_OAUTH_CLIENT_ID` / `TS_OAUTH_CLIENT_SECRET` live in **that
runner's environment** — not in GitHub, and not in the shared runner pool (whose env any private-repo
job could read). One-time setup:

1. Create a read-only (`all:read`) OAuth client in the Tailscale admin console.
2. Stand up a dedicated runner service carrying its id/secret in the env and the `tailscale-api` label
   (a scoped, deliberate exception to the runner box's "no secrets on the box" rule — minimal blast
   radius since the client is read-only).
3. Set the repo **Variable** `TS_TAILNET` (the tailnet name is not a secret).

The lane self-skips cleanly if the runner env vars are absent. Optionally set the `ANTHROPIC_API_KEY`
**secret** to enable Claude enrichment on the spec-drift and live lanes (all lanes degrade gracefully
without it; the client-lib lane never receives the key by design, since it builds untrusted upstream code).

## License

TBD.
