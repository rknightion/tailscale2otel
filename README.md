# tailscale2otel — Tailscale → OpenTelemetry & Prometheus exporter

[![Release](https://img.shields.io/github/v/release/rknightion/tailscale2otel?logo=github&label=release)](https://github.com/rknightion/tailscale2otel/releases/latest)
[![CI](https://github.com/rknightion/tailscale2otel/actions/workflows/ci.yml/badge.svg)](https://github.com/rknightion/tailscale2otel/actions/workflows/ci.yml)
[![Container](https://img.shields.io/badge/ghcr.io-tailscale2otel-2496ED?logo=docker&logoColor=white)](https://github.com/rknightion/tailscale2otel/pkgs/container/tailscale2otel)
[![Helm chart](https://img.shields.io/badge/helm-OCI%20chart-0F1689?logo=helm&logoColor=white)](https://github.com/rknightion/tailscale2otel/pkgs/container/charts%2Ftailscale2otel)
[![Go Reference](https://pkg.go.dev/badge/github.com/rknightion/tailscale2otel/v5.svg)](https://pkg.go.dev/github.com/rknightion/tailscale2otel/v5)
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
| **332** metrics + **30** log-event types | across **17** collectors |
| **19** Tailscale API endpoints consumed | polled, streamed, or webhook-driven |
| **134** shipped rules | **111** alert + **23** recording, Grafana-managed |
| **2** Grafana dashboards | tailnet + exporter health, v2 dynamic (Grafana 13+) |
| **OTLP** push (gRPC/HTTP) | **and/or** a Prometheus pull endpoint |

## Why this exists

Tailscale — the WireGuard-based mesh VPN — exposes a broad observability surface: network
flow logs, configuration audit logs, a detailed device inventory, users, keys, DNS, ACL policy,
device posture. But it has **no Prometheus endpoint of its own**, and it streams logs only to
SIEM/storage sinks like Splunk or S3.
The existing Tailscale exporters cover a slice of the device API and stop there.

`tailscale2otel` turns those sources into
[semantic-convention](https://opentelemetry.io/docs/specs/semconv/)-compliant OTEL telemetry. Its
cardinality controls keep flow-log metrics within a fixed series budget.

### What it adds

- **Network flow logs as *both* metrics and logs.** Low-cardinality aggregate counters
  (`tailscale.network.io` / `.packets` / `.flows`) for dashboards and alerting, **plus** full-fidelity
  per-connection records as OTEL logs for drill-down — with a top-N rollup (busiest 500 pairs, rest
  folded to `__other__`), opt-in port dimensions, and IANA service-name attribution so
  `dst.port: 443` becomes `https`. Without the rollup, each endpoint pair becomes another metric
  series and the cost grows with the traffic graph.
- **Configuration audit logs → structured OTEL logs + a curated, security-categorized change
  counter**, so you can alert on high-value tailnet changes without ingesting the whole stream.
- **Central `tailscaled` node-metrics polling.** Scrapes each node's native client-metrics endpoint
  (`:5252`) from one place instead of deploying a scraper per node — with **automatic target
  discovery from the devices API** (tag include/exclude, online-only, address family). Emits both the
  raw `tailscaled_*` series and 8 curated `tailscale.node.*` metrics with folded low-cardinality
  attributes.
- **Beyond the device API.** Users, auth keys / OAuth clients / API tokens
  (with expiry), tailnet settings, DNS, ACL policy (scored for structural risk: wildcards,
  unrestricted rules, auto-approvers, SSH wildcards), device posture / MDM integrations, Tailscale
  Services, webhook endpoints, contacts, log-stream delivery health, and OAuth apps.
- **Four ingestion paths into one pipeline** — poll the API, receive Tailscale's log stream on a
  built-in Splunk-HEC-compatible receiver, read Tailscale's flow-log export straight out of an
  S3-compatible bucket, or take real-time HMAC-verified webhooks. All four feed the same processors.
- **Offline GeoIP and ASN enrichment of external peers.** Optional, from MaxMind `.mmdb` files on
  local disk — no hosted lookup service, no per-address network call on the hot path. Country and
  continent are bounded and can go on flow metrics; the autonomous system (and, with a City database,
  locality and coordinates) ride the flow logs, where a breakdown costs nothing. Databases hot-swap on
  a schedule, with a built-in MaxMind updater if you want one. Tailnet addresses are never geolocated.
- **Multi-tailnet / MSP mode** — one process observing N tailnets, each with its own credentials, and
  `tailscale.tailnet` as a real label on every signal (no `target_info` join required).
- **PII redaction on by default** — 13 opt-out categories covering emails, user IDs, hostnames, IPs,
  node IDs and free-text detail, applied to metric attributes, log bodies *and* span attributes.
- **API drift CI.** Tailscale's API "may change or break without notice", so a decode-fuzz lane
  gates every PR and three scheduled lanes diff the live OpenAPI spec, track the upstream client
  library, and hit the real API read-only. See [API drift CI](#api-drift-ci).

## Start with a destination

- **[Grafana Cloud over OTLP](https://m7kni.io/tailscale2otel/getting-started/#grafana-cloud-over-otlp)**
  pushes OpenTelemetry data to Grafana Cloud.
- **[Prometheus pull](https://m7kni.io/tailscale2otel/getting-started/#prometheus-pull)** exposes
  `/metrics` for a scraper you already operate.
- **[stdout](https://m7kni.io/tailscale2otel/getting-started/#stdout)** prints telemetry locally,
  with no observability backend.

Ready-made configuration starters live under [`examples/config/`](./examples/config/):
[Grafana Cloud OTLP](./examples/config/grafana-cloud-otlp.yaml),
[Prometheus pull](./examples/config/prometheus-only.yaml), [stdout](./examples/config/stdout.yaml),
[Headscale](./examples/config/headscale.yaml), and
[multi-tailnet/MSP](./examples/config/multi-tailnet.yaml). They keep credentials empty and
document the environment or mounted-file forms to fill in.

The canonical runnable Docker, Compose, Helm, and binary commands live in
[Getting Started](https://m7kni.io/tailscale2otel/getting-started/). It explains the terms, how to
supply Tailscale authentication, and what result proves each path works. For installation details
such as release binaries, persistence, and secret mounts, see
[Installation](https://m7kni.io/tailscale2otel/installation/).

## Where the telemetry goes

- **OTLP push** (`otlp.protocol: grpc|http`) with first-class Grafana Cloud support — set
  `otlp.grafana_cloud.{instance_id,token}` and the Basic-auth header is built for you. Full TLS/mTLS
  knobs. Metrics and logs always; **traces are opt-in** (`tracing.enabled`) for the exporter's own
  self-observability, with exemplars linking API-duration histograms to the originating span.
- **Prometheus pull endpoint** (`delivery.mode: prometheus` or `prometheus.enabled`) — `GET /metrics`
  on its own loopback-only listener by default (`127.0.0.1:2112`), with bearer/basic auth and TLS
  available for remote scrapers. Use it if you already run Prometheus and don't want an OTLP pipeline.
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
| `k8s_audit` | 60s | **(opt-in)** tsrecorder Kubernetes API requests and terminal sessions from object storage |
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

### Logs: poll, stream *or* object store — pick one

Both `flowlogs` and `auditlogs` take a `source` of `poll` (default), `stream`, `objectstore` or
`both`. Tailscale exports each log type to object storage independently, so each has its OWN
destination (`collectors.flowlogs.objectstore` / `collectors.auditlogs.objectstore`) and nothing is
inherited between them. **Pick exactly one method per log type** — running two risks
double-counting, cross-source de-dup is only a best-effort failsafe, and the exporter WARNs at startup
when it sees this.

```yaml
# Poll: tailscale2otel pulls on a schedule (interval/lag/initial_lookback/max_window apply).
flowlogs: { enabled: true, source: poll, interval: 60s, lag: 120s, initial_lookback: 5m, max_window: 1h }

# Stream: Tailscale pushes to the built-in HEC receiver (the window fields are ignored).
flowlogs: { enabled: true, source: stream, log_mode: per_connection }

# Object store: read the export Tailscale writes to S3. No API quota, and the cheapest path for a
# busy tailnet. Credentials come from the ambient chain (env, IRSA, instance profile).
flowlogs:
  enabled: true
  source: objectstore
  objectstore: { endpoint: https://s3.eu-west-2.amazonaws.com, region: eu-west-2, bucket: my-flow-logs }
```

Object-store delivery is at-least-once. With the file checkpoint store, successful object identities
and failed-object gaps survive restart; transient failures retry with bounded backoff, while invalid
compressed objects are quarantined for operator acknowledgement. One object is all-or-nothing — every
row is decoded before any is committed, so a mid-object failure emits nothing rather than a partial
prefix — but the object as a unit replays if the process dies between emission and the checkpoint
write. OTLP/backend acknowledgement is outside this boundary.

**Backfill has a hard ceiling of 14 day partitions** — today plus the previous 13 days — under the
default `layout: partitioned`, whatever `initial_lookback` says. It is permanent, not per-cycle: one
cycle enumerates at most 14 day prefixes newest-first, and the cursor only moves forward, so older
days are never listed and are skipped with no gap, no error and no metric. `layout: flat` has no
partitions to cap and reaches arbitrarily far back, at the cost of more LIST requests. The exporter
warns at startup when `initial_lookback` exceeds the ceiling. See
[Streaming & webhooks](https://m7kni.io/tailscale2otel/streaming-webhooks/) for the full path-by-path
compatibility, delivery and durability matrix.

Checkpoints persist how far poll and object-store collectors have read. Details on all paths,
receiver auth, object-gap handling, and `auto_configure` are in
[Streaming & webhooks](https://m7kni.io/tailscale2otel/streaming-webhooks/).

## Dashboards, alerts & the admin UI

- **Dashboards** — [`deploy/grafana/`](./deploy/grafana/) ships 2 dashboards on Grafana's **v2
  schema** (Grafana 13+): **Tailnet** (is my tailnet healthy — devices, network, security, policy)
  and **Exporter health** (is the exporter healthy — collection, ingestion, delivery, runtime, cost),
  cross-linked to each other, with dynamic rendering so a section only appears when its data is
  present. **Grafana 13+ is a hard requirement** — 12.4 accepts the file with a `200` and renders
  nothing, and 11.5 rejects it with the misleading `Dashboard title cannot be empty`. This
  repository publishes them through GitSync; other deployments can import or provision the two JSON
  resources. See [Dashboards](https://m7kni.io/tailscale2otel/dashboards/).
- **Alerts** — [`deploy/alerts/grafana-managed/`](./deploy/alerts/grafana-managed/) ships **111 alert
  rules and 23 recording rules** (134 total) as `rules.alerting.grafana.app` manifests, one JSON per
  rule. Push them with `gcx resources push -p deploy/alerts/grafana-managed`. Every alert carries a
  `runbook_url`, and 107 of 111 link a canonical dashboard panel. See
  [Alerts](https://m7kni.io/tailscale2otel/alerts/) and
  [Runbooks](https://m7kni.io/tailscale2otel/runbooks/).
- **Admin status page** — on by default at `127.0.0.1:9091`. Liveness/readiness probes at `/healthz` and
  `/readyz` (never auth-gated), a live status page at `/`, and the same snapshot at
  `/api/status.json`: per-collector health, **active-series cardinality** with per-label breakdown,
  the full metrics/log catalog, discovered node targets, and a **redacted** config summary. Entirely
  self-contained — no CDN assets, so it renders on an air-gapped tailnet. Auth **fails closed** on a
  non-loopback bind with no `admin.auth.token`.
- **Continuous profiling** is opt-in — pprof on the admin server (for Grafana Alloy to pull), or
  push to Pyroscope / Grafana Cloud Profiles.

## Configuration

Layered, lowest precedence first: **built-in defaults** → **optional YAML file** → **environment
variables**. Scalar and simple-list fields are settable as `TS2OTEL_` + the dotted key path with
`__` between levels; maps and lists of structured entries stay in YAML:

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
| [Metrics catalog](https://m7kni.io/tailscale2otel/metrics/) | All 332 metrics and 30 log events |
| [Node metrics](https://m7kni.io/tailscale2otel/node-metrics/) | Central `tailscaled` scraping |
| [Streaming & webhooks](https://m7kni.io/tailscale2otel/streaming-webhooks/) | HEC receiver and webhooks |
| [Architecture](https://m7kni.io/tailscale2otel/architecture/) | How it fits together |
| [Security](https://m7kni.io/tailscale2otel/security/) | Data handling, PII, receiver auth |
| [Troubleshooting](https://m7kni.io/tailscale2otel/troubleshooting/) | When it doesn't work |
| [Runbooks](https://m7kni.io/tailscale2otel/runbooks/) | Alert investigation and remediation |

## Development

```sh
go build ./... && go vet ./... && go test -race ./...
golangci-lint run
```

Small single-purpose packages under `internal/`: `telemetry` (OTEL facade), `collector`
(scheduler/registry/checkpoints + one package per source), `tsapi` (Tailscale client),
`provider`/`hsapi` (control-plane abstraction + Headscale), `flowlog`/`audit` (records + processors),
`enrich` (device cache), `rdns`, `config`, and the `stream`/`webhook` receivers. Four committed files
are generated — run `just gen` before committing changes that touch them.

## API drift CI

Tailscale's API and OpenAPI spec evolve continuously ("may change or break without notice"), which
has broken decoders here before. Eight lanes guard it:

| Lane | When | What it checks |
|---|---|---|
| **Schema-driven decode tests** | every PR (gates) | synthesizes payloads from the vendored OpenAPI spec + known wire quirks (numeric `proto`, polymorphic audit `old`/`new`) through the real decoders, plus a **boundary matrix** running every consumed operation against every boundary shape — null, empty container, nulled nullable fields, extreme values, an unknown enum member, an additive field, and a wrong container shape that must be *rejected*. Runs inside the normal `go test -race ./...` leg, which `ci-success` requires |
| **Exploratory fuzzing** | every PR (advisory) | `go test -fuzz` over the HEC envelope, HEC timestamps and the flow/audit decoders. Deliberately **not** required: finding a NEW crasher is nondeterministic, so gating it would let an unrelated PR randomly block merges. Each target's seed corpus runs in the gated leg above, so a KNOWN crasher still blocks |
| **OpenAPI drift** | daily | diffs the live spec against the vendored copy, scoped to consumed operations. Covers response fields, path/query/header **parameters** (requiredness, type, default, enum), the **success-status set** and **request/response media types**, classifying each as breaking, behavioral or additive |
| **Client-lib tracking** | weekly | builds and tests against `tailscale-client-go/v2@main` and `@latest` |
| **Scheduled fuzzing** | weekly | the same nine fuzz targets for 15 minutes each instead of 120 seconds, where a nondeterministic finding costs nobody a blocked merge. Opens a deduplicated tracking issue on a crasher and attaches the failing input |
| **Live contract** | daily | hits the real API read-only and asserts every consumed GET still decodes |
| **Changelog review** | monthly | reads Tailscale's changelog feed for entries that name something this exporter collects and carry no recorded verdict in `spec/changelog-reviewed.json`. Catches a capability announced *before*, or without, any OpenAPI change. Reviewing an entry means recording a verdict — including a negative one, so a surface already declined is never re-proposed |
| **IANA registry freshness** | monthly | regenerates the embedded IANA service-name table from the live registry and reports a diff. The committed copy has no other drift gate and its staleness is invisible at runtime — an unregistered port and a port missing from a stale table both map to no service name |
| **Release completeness** | every release | reads the published release back and fails when its asset manifest is short. Two releases shipped permanently incomplete behind green workflows before this existed |

Scheduled lanes are advisory — they open a deduplicated tracking issue and fail the run, but never
block PRs. Exploratory fuzzing is advisory too, for the reason in its row, so it runs on pushes to
`main` rather than on pull requests; of the PR-time lanes, the schema-driven decode tests gate. The
seed corpora ride `go test -race`, so a *known* crasher still blocks a merge.

The cadences and the advisory-versus-gating split in this table are asserted by
`internal/ci/workflowcontract_test.go`, which reads the workflow files — two of these rows claimed
"weekly" against daily crons until that test was added (#436).

<details>
<summary>Maintainer one-time setup</summary>

```sh
gh label create api-drift -c FBCA04
gh label create clientlib-drift -c FBCA04
gh label create live-contract -c FBCA04
```

The live lane stores **no long-lived Tailscale key**. It runs on a standard GitHub-hosted runner and
mints a short-lived token from Tailscale's OAuth endpoint using a read-only (`all:read`) OAuth client,
whose `TS_OAUTH_CLIENT_ID` / `TS_OAUTH_CLIENT_SECRET` are repo secrets. Keeping them as secrets is safe
because this lane is `schedule` + `workflow_dispatch` only, so a fork PR can never run it and never
reach them; the minted token is masked and lives only for that run. Set the repo variable `TS_TAILNET`
(the tailnet name is not a secret). Missing configuration fails the lane loudly rather than
self-skipping, so a misconfigured preflight cannot look green. Optionally set the `ANTHROPIC_API_KEY` secret
for Claude enrichment on the spec-drift and live lanes; the client-lib lane never receives it by
design, since it builds untrusted upstream code.

</details>

## License

Apache License 2.0 — full text in [`LICENSE`](./LICENSE); third-party attribution and bundled
notices/SBOMs in [`LICENSING.md`](./LICENSING.md).
