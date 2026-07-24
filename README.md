# tailscale2otel — Tailscale → OpenTelemetry & Prometheus exporter

[![Release](https://img.shields.io/github/v/release/rknightion/tailscale2otel?logo=github&label=release)](https://github.com/rknightion/tailscale2otel/releases/latest)
[![CI](https://github.com/rknightion/tailscale2otel/actions/workflows/ci.yml/badge.svg)](https://github.com/rknightion/tailscale2otel/actions/workflows/ci.yml)
[![Container](https://img.shields.io/badge/ghcr.io-tailscale2otel-2496ED?logo=docker&logoColor=white)](https://github.com/rknightion/tailscale2otel/pkgs/container/tailscale2otel)
[![Helm chart](https://img.shields.io/badge/helm-OCI%20chart-0F1689?logo=helm&logoColor=white)](https://github.com/rknightion/tailscale2otel/pkgs/container/charts%2Ftailscale2otel)
[![Go Reference](https://pkg.go.dev/badge/github.com/rknightion/tailscale2otel/v2.svg)](https://pkg.go.dev/github.com/rknightion/tailscale2otel/v2)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/rknightion/tailscale2otel/badge)](https://scorecard.dev/viewer/?uri=github.com/rknightion/tailscale2otel)
[![License](https://img.shields.io/github/license/rknightion/tailscale2otel)](./LICENSE)

**A single Go binary that turns your Tailscale tailnet into OpenTelemetry metrics, logs and traces
over OTLP — or a Prometheus `/metrics` endpoint, or both at once.** Network flow logs, configuration
audit logs, device fleet health, key expiry, ACL risk, and `tailscaled` per-node metrics, exported to
Grafana Cloud or any OTEL backend. [Headscale](https://headscale.net/) is supported too.

📖 **Full documentation: [m7kni.io/tailscale2otel](https://m7kni.io/tailscale2otel/)** —
[Getting started](https://m7kni.io/tailscale2otel/getting-started/) ·
[Installation](https://m7kni.io/tailscale2otel/installation/) ·
[Configuration](https://m7kni.io/tailscale2otel/configuration/) ·
[Metrics catalog](https://m7kni.io/tailscale2otel/metrics/)

| | |
|---|---|
| **186** metrics + **13** log-event types | across **15** collectors |
| **18** Tailscale API endpoints consumed | polled, streamed, or webhook-driven |
| **89** shipped alert rules | 79 Grafana-managed + 10 Prometheus |
| **5** Grafana dashboards | 1 flagship 10-tab + 4 legacy-schema |
| **OTLP** push (gRPC/HTTP) | **and/or** a Prometheus pull endpoint |

## Why this exists

Tailscale — the WireGuard-based mesh VPN — exposes a genuinely rich observability surface: network
flow logs, configuration audit logs, a detailed device inventory, users, keys, DNS, ACL policy,
device posture. But it has **no Prometheus endpoint of its own**, and it streams logs only to
SIEM/storage sinks like Splunk or S3.
The existing Tailscale exporters cover a slice of the device API and stop there.

`tailscale2otel` covers the whole surface and models it properly:
[semantic-convention](https://opentelemetry.io/docs/specs/semconv/)-compliant OTEL telemetry, with
cardinality control that makes flow logs survivable on a metrics backend.

### Things nothing else does

- **Network flow logs as *both* metrics and logs.** Low-cardinality aggregate counters
  (`tailscale.network.io` / `.packets` / `.flows`) for dashboards and alerting, **plus** full-fidelity
  per-connection records as OTEL logs for drill-down — with a top-N rollup (busiest 500 pairs, rest
  folded to `__other__`), opt-in port dimensions, and IANA service-name attribution so
  `dst.port: 443` becomes `https`. This is the feature that usually makes flow logs unaffordable, and
  it is the reason this project exists.
- **Configuration audit logs → structured OTEL logs + a curated, security-categorized change
  counter**, so you can alert on high-value tailnet changes without ingesting the whole stream.
- **Central `tailscaled` node-metrics polling.** Scrapes each node's native client-metrics endpoint
  (`:5252`) from one place instead of deploying a scraper per node — with **automatic target
  discovery from the devices API** (tag include/exclude, online-only, address family). Emits both the
  raw `tailscaled_*` series and 8 curated `tailscale.node.*` metrics with folded low-cardinality
  attributes.
- **Full API-surface coverage** — not just devices. Users, auth keys / OAuth clients / API tokens
  (with expiry), tailnet settings, DNS, ACL policy (scored for structural risk: wildcards,
  unrestricted rules, auto-approvers, SSH wildcards), device posture / MDM integrations, Tailscale
  Services, webhook endpoints, contacts, log-stream delivery health, and OAuth apps.
- **Three ingestion paths into one pipeline** — poll the API, receive Tailscale's log stream on a
  built-in Splunk-HEC-compatible receiver, or take real-time HMAC-verified webhooks. All three feed
  the same processors.
- **Multi-tailnet / MSP mode** — one process observing N tailnets, each with its own credentials, and
  `tailscale.tailnet` as a real label on every signal (no `target_info` join required).
- **PII redaction on by default** — 13 opt-out categories covering emails, user IDs, hostnames, IPs,
  node IDs and free-text detail, applied to metric attributes, log bodies *and* span attributes.
- **API drift CI.** Tailscale's API "may change or break without notice", so a decode-fuzz lane
  gates every PR and three scheduled lanes diff the live OpenAPI spec, track the upstream client
  library, and hit the real API read-only. See [API drift CI](#api-drift-ci).

## Quick start

### Docker

```sh
docker run --rm \
  -e TS2OTEL_TAILSCALE__TAILNET=example.com \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id> \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token> \
  ghcr.io/rknightion/tailscale2otel:latest
```

No config file needed — every setting has a `TS2OTEL_*` environment variable. Mount a YAML file and
pass `-config /etc/tailscale2otel/config.yaml` if you prefer.

### Kubernetes (Helm)

```sh
helm install tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
  --set-string config.tailscale.tailnet=example.com \
  --set-string secrets.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id> \
  --set-string secrets.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret>
```

See [Installation](https://m7kni.io/tailscale2otel/installation/) for docker-compose, prebuilt
binaries (Linux/macOS/Windows × amd64/arm64), and the full chart values.

### Binary

```sh
go build -o tailscale2otel ./cmd/tailscale2otel
cp config.example.yaml config.yaml   # then edit; secrets stay in env vars
./tailscale2otel -config config.yaml

./tailscale2otel -version                      # print version and exit
./tailscale2otel -validate -config config.yaml # lint a config without starting
```

### No backend? Run it locally

Set `TS2OTEL_OTLP__PROTOCOL=stdout` to print metrics and logs to the console.

## Where the telemetry goes

- **OTLP push** (`otlp.protocol: grpc|http`) with first-class Grafana Cloud support — set
  `otlp.grafana_cloud.{instance_id,token}` and the Basic-auth header is built for you. Full TLS/mTLS
  knobs. Metrics and logs always; **traces are opt-in** (`tracing.enabled`) for the exporter's own
  self-observability, with exemplars linking API-duration histograms to the originating span.
- **Prometheus pull endpoint** (`prometheus.enabled`, off by default) — `GET /metrics` on its own
  dedicated listener (default `:2112`), served *alongside* OTLP push, with optional bearer/basic auth
  and TLS. Use it if you already run Prometheus and don't want an OTLP pipeline.
- **stdout** for local debugging.

> **OTLP→Prometheus naming:** query the *normalized* name. Dots become underscores, monotonic
> counters gain `_total`, units suffix (`By`→`_bytes`, `s`→`_seconds`), and a unit-`1` gauge gains
> `_ratio` — so `tailscale.network.io` → `tailscale_network_io_bytes_total`. The full mapping is in
> the [metrics catalog](https://m7kni.io/tailscale2otel/metrics/).

## Collectors

| Collector | Cadence | Emits |
|---|---|---|
| `devices` | 60s | online/last-seen/key-expiry/update gauges, NAT & connectivity quality, per-DERP latency, subnet routes, tailnet lock, fleet hygiene roll-ups. **Feeds the enrichment cache** |
| `flowlogs` | 60s | aggregated traffic counters + per-connection flow logs |
| `auditlogs` | 60s | audit-event logs + a categorized change counter |
| `users` | 300s | user/role/status counts, per-user device & connection gauges, outstanding invites |
| `keys` | 300s | expiry gauges and counts across auth keys, OAuth clients and API tokens |
| `oauth_apps` | 300s | OAuth-application inventory (alpha API; idles silently where unavailable) |
| `settings` | 600s | tailnet feature-toggle gauges |
| `acl` | 600s | ACL size, change detection (by ETag), structural risk scoring |
| `dns` | 600s | nameserver / search-path / split-zone counts, MagicDNS flag |
| `contacts` | 600s | contact verification status (the email itself is never emitted) |
| `webhooks` | 600s | webhook-endpoint inventory + per-endpoint subscription counts |
| `posture_integrations` | 600s | MDM/EDR integration counts, sync health, matched devices |
| `log_stream` | 600s | Tailscale's own SIEM-sink delivery health + delivery counters |
| `services` | 600s | Tailscale Services (VIP) inventory — counts, ports, opt-in backing hosts |
| `node_metrics` | 60s | **(opt-in)** scrapes `tailscaled` `/metrics` endpoints; see above |

Each can be disabled or re-tuned. Under `provider: headscale` the Tailscale-only collectors
auto-disable and a reduced set (devices, users, keys, ACL, node-metrics) runs.

**Device enrichment depends on the `devices` collector** — flow/audit IP→name resolution silently
degrades to `unknown`/`external` without it.

### Logs: poll *or* stream, pick one

`flowlogs` and `auditlogs` each take a `source` of `poll` (default), `stream`, or `both`. **Pick
exactly one method per log type** — `both` risks double-counting, cross-source de-dup is only a
best-effort failsafe, and the exporter WARNs at startup when it sees this.

```yaml
# Poll: tailscale2otel pulls on a schedule (interval/lag/initial_lookback/max_window apply).
flowlogs: { enabled: true, source: poll, interval: 60s, lag: 120s, initial_lookback: 5m, max_window: 1h }

# Stream: Tailscale pushes to the built-in HEC receiver (the window fields are ignored).
flowlogs: { enabled: true, source: stream, log_mode: per_connection }
```

Checkpoints persist how far each *polled* collector has read so restarts resume without gaps. Details
on both paths, receiver auth, and `auto_configure` are in
[Streaming & webhooks](https://m7kni.io/tailscale2otel/streaming-webhooks/).

## Dashboards, alerts & the admin UI

- **Dashboards** — [`deploy/grafana/`](./deploy/grafana/) ships a flagship **10-tab** dashboard
  (Overview, Fleet & Devices, Network & Flows, Events & Logs, Security & Audit, Policy & Config, Node
  Metrics, Tailnets, Exporter Diagnostics, Cardinality & Cost) on Grafana's **v2 schema** (Grafana
  13+), with dynamic rendering so a section only appears when its data is present — plus 4 standalone
  legacy-schema dashboards for older stacks. See
  [Dashboards](https://m7kni.io/tailscale2otel/dashboards/).
- **Alerts** — [`deploy/alerts/`](./deploy/alerts/) ships 79 Grafana-managed rules and 10 Prometheus
  rules. See [Alerts](https://m7kni.io/tailscale2otel/alerts/).
- **Admin status page** — on by default at `:9091`. Liveness/readiness probes at `/healthz` and
  `/readyz` (never auth-gated), a live status page at `/`, and the same snapshot at
  `/api/status.json`: per-collector health, **active-series cardinality** with per-label breakdown,
  the full metrics/log catalog, discovered node targets, and a **redacted** config summary. Entirely
  self-contained — no CDN assets, so it renders on an air-gapped tailnet. Auth **fails closed** on a
  non-loopback bind with no `admin.auth.token`.
- **Continuous profiling** is opt-in — pprof on the admin server (for Grafana Alloy to pull), or
  push to Pyroscope / Grafana Cloud Profiles.

## Configuration

Layered, lowest precedence first: **built-in defaults** → **optional YAML file** → **environment
variables**. Every field is settable as `TS2OTEL_` + the dotted key path with `__` between levels:

| Config key | Environment variable |
|---|---|
| `tailscale.auth.oauth.client_secret` | `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET` |
| `otlp.endpoint` | `TS2OTEL_OTLP__ENDPOINT` |
| `collectors.flowlogs.interval` | `TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL` |

An unrecognised `TS2OTEL_*` variable is logged as a **WARN** at startup — usually a typo.

**Authentication:** prefer an [OAuth client](https://tailscale.com/kb/1215/oauth-clients)
(auto-refreshing, least-privilege `all:read`) over an API key. Keyless **workload identity** (OIDC
token exchange, e.g. a Kubernetes projected service-account token) is also supported, and every
secret has a `*_file` variant for Docker/Kubernetes secrets.

→ [Full configuration reference](https://m7kni.io/tailscale2otel/configuration/) ·
[every `TS2OTEL_*` variable](https://m7kni.io/tailscale2otel/env-vars/) ·
[`config.example.yaml`](./config.example.yaml)

## Documentation

| | |
|---|---|
| [Getting started](https://m7kni.io/tailscale2otel/getting-started/) | Zero to first metrics in Grafana Cloud |
| [Installation](https://m7kni.io/tailscale2otel/installation/) | Docker, Helm, compose, binaries |
| [Configuration](https://m7kni.io/tailscale2otel/configuration/) | Every key, default and gotcha |
| [Metrics catalog](https://m7kni.io/tailscale2otel/metrics/) | All 186 metrics and 13 log events |
| [Node metrics](https://m7kni.io/tailscale2otel/node-metrics/) | Central `tailscaled` scraping |
| [Streaming & webhooks](https://m7kni.io/tailscale2otel/streaming-webhooks/) | HEC receiver and webhooks |
| [Architecture](https://m7kni.io/tailscale2otel/architecture/) | How it fits together |
| [Security](https://m7kni.io/tailscale2otel/security/) | Data handling, PII, receiver auth |
| [Troubleshooting](https://m7kni.io/tailscale2otel/troubleshooting/) | When it doesn't work |

## Development

```sh
go build ./... && go vet ./... && go test -race ./...
golangci-lint run
```

Small single-purpose packages under `internal/`: `telemetry` (OTEL facade), `collector`
(scheduler/registry/checkpoints + one package per source), `tsapi` (Tailscale client),
`provider`/`hsapi` (control-plane abstraction + Headscale), `flowlog`/`audit` (records + processors),
`enrich` (device cache), `rdns`, `config`, and the `stream`/`webhook` receivers. Four committed files
are generated — run `scripts/regen-generated.sh` before committing changes that touch them.

## API drift CI

Tailscale's API and OpenAPI spec evolve continuously ("may change or break without notice"), which
has broken decoders here before. Four lanes guard it:

| Lane | When | What it checks |
|---|---|---|
| **Schema-driven decode fuzz** | every PR (gates) | synthesizes payloads from the vendored OpenAPI spec + known wire quirks (numeric `proto`, polymorphic audit `old`/`new`) through the real decoders |
| **OpenAPI drift** | weekly | diffs the live spec against the vendored copy, scoped to consumed operations, classifying breaking vs informational |
| **Client-lib tracking** | weekly | builds and tests against `tailscale-client-go/v2@main` and `@latest` |
| **Live contract** | weekly | hits the real API read-only and asserts every consumed GET still decodes |

Scheduled lanes are advisory — they open a deduplicated tracking issue and fail the run, but never
block PRs. Only the decode-fuzz lane gates.

<details>
<summary>Maintainer one-time setup</summary>

```sh
gh label create api-drift -c FBCA04
gh label create clientlib-drift -c FBCA04
gh label create live-contract -c FBCA04
```

The live lane stores **no Tailscale key in GitHub**. It runs on a dedicated self-hosted runner (label
`tailscale-api`) and mints a short-lived token from Tailscale's OAuth endpoint using a read-only
(`all:read`) OAuth client whose `TS_OAUTH_CLIENT_ID` / `TS_OAUTH_CLIENT_SECRET` live in that runner's
environment. Set the repo variable `TS_TAILNET` (the tailnet name is not a secret). The lane
self-skips cleanly if the runner env vars are absent. Optionally set the `ANTHROPIC_API_KEY` secret
for Claude enrichment on the spec-drift and live lanes; the client-lib lane never receives it by
design, since it builds untrusted upstream code.

</details>

## License

Apache License 2.0 — full text in [`LICENSE`](./LICENSE); third-party attribution and bundled
notices/SBOMs in [`LICENSING.md`](./LICENSING.md).
