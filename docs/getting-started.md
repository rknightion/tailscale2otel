---
title: Getting Started
description: Choose Grafana Cloud OTLP, Prometheus pull, or stdout and reach a first observable signal with tailscale2otel.
tags:
  - Getting Started
  - Prometheus
---

# Getting Started

This guide gets one first signal from your tailnet. Choose a destination first; the same Tailscale
authentication works for all three paths.

## Six terms before you start

- **OpenTelemetry** is a common format and SDK for observability data: metrics, logs, and traces.
- **OTLP** is OpenTelemetry's wire protocol. It pushes that data from the exporter to a receiver.
- An **OpenTelemetry Collector** (or Grafana Alloy) is an optional relay: it receives OTLP, can
  process or buffer it, and sends it to a backend. It is useful for a shared gateway, but not a
  prerequisite for this exporter.
- **Grafana Cloud** is one hosted backend that accepts OTLP. It provides the endpoint and access
  token used by the Grafana route below.
- **Prometheus pull** works the other direction: your Prometheus-compatible scraper requests this
  program's `/metrics` endpoint on a schedule.
- **stdout** is the terminal. It is a debugging destination, not a backend: telemetry is printed
  where the process runs and is not retained by the exporter.

## Choose a destination

Pick exactly one route for a first run:

| Destination | Use it when | First proof |
|---|---|---|
| [Grafana Cloud over OTLP](#grafana-cloud-over-otlp) | Grafana Cloud or another OTLP receiver is your destination. | The one-shot run exits successfully and the signal is queryable in the backend. |
| [Prometheus pull](#prometheus-pull) | You already run a scraper. | `/metrics` answers locally, then the scraper reports the target up. |
| [stdout](#stdout) | You want to prove collection without a backend. | The one-shot run prints telemetry in the terminal. |

Do not configure OTLP push and have a Prometheus scraper send the same metrics to the same backend
unless you deliberately want two copies. [`delivery.mode`](configuration.md#delivery-modes) makes
the safer choices explicit: `prometheus` turns inherited OTLP export off, while `dual` is only for
separate destinations.

## Prerequisites

Every route needs:

- A **Tailscale tailnet** you control.
- **Authentication credentials** — an OAuth client is strongly preferred:
    - **OAuth client (recommended):** create one in the [Tailscale admin
      console](https://tailscale.com/kb/1215/oauth-clients) with the least-privilege read scopes
      your collectors need (at minimum `all:read`). OAuth tokens are short-lived, auto-refreshed,
      and not tied to a user account.
    - **API key (fallback):** a personal API key also works (`method: apikey`), but it expires in
      90 days or less and is tied to its creator — the exporter logs a warning when one is
      configured.

!!! tip "Running Headscale instead of Tailscale?"
    tailscale2otel also supports a self-hosted [Headscale](https://headscale.net/) control plane —
    set `provider: headscale` and point it at your server instead of the steps below. A reduced
    collector set runs automatically (devices, users, keys, ACL, node-metrics); see
    [Configuration → `headscale`](configuration.md#headscale-headscale-control-plane-connection)
    for the connection settings and exactly what's affected.

## Configuration starters

The repository includes small, delivery-specific starters under
[`examples/config/`](../examples/config/). Use
[`headscale.yaml`](../examples/config/headscale.yaml) for a self-hosted Headscale control plane;
it keeps the API key empty and documents the environment or mounted-file form. Use
[`multi-tailnet.yaml`](../examples/config/multi-tailnet.yaml) for MSP mode; the `tailnets:` list is
file-defined and each entry has its own OAuth identity, with a name-keyed environment overlay for
the secret. The existing [Grafana Cloud](../examples/config/grafana-cloud-otlp.yaml),
[Prometheus](../examples/config/prometheus-only.yaml), and
[stdout](../examples/config/stdout.yaml) starters cover the delivery choices below.

All starters are hand-maintained examples, not copies of the exhaustive
[`config.example.yaml`](../config.example.yaml). Keep their secret fields empty and run
`-validate` followed by `-preflight` after supplying real credentials.

## Tailscale authentication

The config file is entirely optional — `tailscale2otel` runs from built-in defaults plus
environment variables. All three starters leave their secret fields empty so these environment
variables supply the shared Tailscale credentials:

```sh
export TS2OTEL_TAILSCALE__TAILNET=example.com  # or leave as "-" for the auth principal's default tailnet
export TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id>
export TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret>
```

These map to `tailscale.tailnet`, `tailscale.auth.oauth.client_id`, and
`tailscale.auth.oauth.client_secret`. The `TS2OTEL_` prefix + `__` between nesting levels is the universal
convention — see [Configuration](configuration.md) for the full mapping rules.

!!! tip "Secrets belong in env vars"
    Keep tokens and client secrets in environment variables only. They never need to appear in a
    YAML file, and a YAML file committed to version control is an easy way to leak credentials.

## Check a configuration before a rollout

Four flags, in increasing order of how much they actually exercise:

- `tailscale2otel -version` prints the build version and exits.
- `tailscale2otel -validate -config <path>` loads and validates a config file through the same
  load/validate path the server uses, without starting the exporter or touching the network at all.
  It prints any advisory warnings and exits 0 if valid, 1 otherwise.
- `tailscale2otel -preflight -config <path>` goes further: it builds the tailnet runtimes,
  authenticates, and runs exactly one collection cycle of every enabled collector, then reports per
  collector and exits. It is side-effect-free by default — no admin, Prometheus, streaming or
  webhook listener is started; nothing is exported; no checkpoint is persisted whatever
  `checkpoint.store` says; profiling stays off; and `collectors.acl.validate` is suppressed because
  it is the one non-`GET` request this exporter makes. The report says so when it does that, so a
  green preflight is never mistaken for proof that the ACL policy validates. Add `-preflight-export`
  to export through the configured OTLP path for real, `-json` for a machine-readable report, and
  `-preflight-timeout` (default `60s`) to bound the run.
- `tailscale2otel -once -config <path>` runs the same single cycle for real: the configured export
  path and checkpoint behaviour both apply, and ACL validation is not suppressed. Still starts no
  listener. Use it to produce one genuine batch of telemetry without running the long-lived server.

```sh
tailscale2otel -preflight -json -config config.yaml
```

Exit codes for `-preflight` and `-once` name the class of problem, because the next action differs
for each: **0** success, **1** the config is invalid, **2** a usage error, **3** the credentials or
endpoint are wrong, **4** the credentials work but a specific collector failed (or the run hit
`-preflight-timeout`), **5** the collectors are fine and the export path is broken. When more than
one class fails, the lowest wins — it is the most upstream cause.

## Grafana Cloud over OTLP

Grafana Cloud accepts pushed OTLP. From its **Connections → OpenTelemetry** page, collect the OTLP
gateway URL, stack/instance ID, and an access-policy token with `metrics:write` and `logs:write`.
The starter's `grafana_cloud` block builds Basic authentication from the ID and token:

```sh
TS2OTEL_OTLP__ENDPOINT=https://otlp-gateway-REGION.grafana.net/otlp \
  TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id> \
  TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token> \
  tailscale2otel -once -config examples/config/grafana-cloud-otlp.yaml
```

The exporter defaults to `otlp.protocol: http` and the example endpoint
`https://otlp-gateway-prod-us-central-0.grafana.net/otlp`. If your stack is in a different region,
replace that example host with the OTLP gateway URL shown for your stack's selected region.
Expected result: the command completes one real collection and export cycle. Query your Grafana
Cloud metrics or logs view for the exporter after it exits. `-preflight` checks authentication and
collection but does not export; use `-preflight-export` if you want its bounded report plus a real
export attempt.

!!! note "Self-hosted Collector or Alloy"
    For a self-hosted OpenTelemetry Collector or Grafana Alloy, set `TS2OTEL_OTLP__PROTOCOL=grpc`
    (or `http`) and point `TS2OTEL_OTLP__ENDPOINT` at your collector's OTLP receiver address — for
    a gateway reachable as `alloy` on the same Docker network or as a Kubernetes Service:

    ```sh
    -e TS2OTEL_OTLP__PROTOCOL=grpc \
    -e TS2OTEL_OTLP__ENDPOINT=alloy:4317 \
    -e TS2OTEL_OTLP__TLS__INSECURE=true
    ```

    Drop the `grafana_cloud` variables when you do this: the backend credential belongs to the
    gateway, not to the exporter. `TS2OTEL_OTLP__TLS__INSECURE` disables transport security
    entirely and is only acceptable on a trusted private hop — which this one is, precisely because
    it now carries no credential.

    `TS2OTEL_OTLP__HEADERS` is a map field and must be set via a config file — see
    [Configuration](configuration.md).

    A complete, validated gateway pipeline — receiver, memory limiter, batch, retry, disk-backed
    sending queue, plus the outage drill to prove it works — is in
    [Collector Gateway](gateway.md).

## Prometheus pull

The Prometheus starter uses `delivery.mode: prometheus`, which enables only the pull listener and
prevents inherited OTLP metrics, logs, and traces from reaching an OTLP backend. It binds to
`127.0.0.1:2112`, so a local scraper needs no token. Run it as a service, then check the endpoint:

```sh
tailscale2otel -config examples/config/prometheus-only.yaml
curl --fail http://127.0.0.1:2112/metrics
tailscale2otel -prometheus-check -config examples/config/prometheus-only.yaml
```

Expected result: `curl` returns Prometheus text and `-prometheus-check` reports a successful,
non-empty first exposition from a side-effect-free collection cycle. Point your scraper at the same
URL; its targets page should then show this target as up. The [scrape
configuration](configuration.md#prometheus-scrape_configs-snippet) is the canonical scraper fragment.

For a remote scraper, set a non-loopback `prometheus.listen` and **one** explicit exposure choice:
`prometheus.auth.token` (or `token_file`) is preferred; `allow_unauthenticated: true` is only for
an intentionally protected network. Verify an authenticated listener with:

```sh
curl --fail -H "Authorization: Bearer $TS2OTEL_PROMETHEUS__AUTH__TOKEN" \
  http://HOST:2112/metrics
```

Do not scrape this endpoint into Grafana Cloud when the exporter is already pushing OTLP there.
Use `delivery.mode: prometheus` for pull-only, or `dual` only when OTLP and pull go to different
destinations. See [Delivery modes](configuration.md#delivery-modes) for explicit per-signal OTLP
opt-back-in.

## stdout

stdout needs no Grafana or Prometheus credential. It is the quickest way to show that Tailscale
authentication and collection work before choosing a backend:

```sh
tailscale2otel -once -config examples/config/stdout.yaml
```

Expected result: the command prints OpenTelemetry-formatted metrics or logs to the terminal during
its collection cycle. It does not retain or send that data anywhere.

## Canonical deployment commands

The destination starters are the canonical configuration for each deployment. Set `CONFIG` to one
of `examples/config/grafana-cloud-otlp.yaml`, `examples/config/prometheus-only.yaml`, or
`examples/config/stdout.yaml`, then use the command for your deployment. Supply the shared Tailscale
environment variables above; the Grafana starter additionally needs its two Grafana variables. Each
parameterized block below is the one canonical executable snippet for all three `CONFIG` choices;
do not copy a second quickstart elsewhere.

### Docker

```sh
CONFIG=examples/config/prometheus-only.yaml
PROMETHEUS_ARGS=()
if [[ "$CONFIG" == "examples/config/prometheus-only.yaml" ]]; then
  PROMETHEUS_ARGS=(
    -p 127.0.0.1:2112:2112
    -e TS2OTEL_PROMETHEUS__LISTEN=:2112
    -e TS2OTEL_PROMETHEUS__AUTH__ALLOW_UNAUTHENTICATED=true
  )
fi
docker run --rm --stop-timeout 45 \
  "${PROMETHEUS_ARGS[@]}" \
  -v "$PWD/$CONFIG:/etc/tailscale2otel/config.yaml:ro" \
  -e TS2OTEL_TAILSCALE__TAILNET \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET \
  ghcr.io/rknightion/tailscale2otel:latest -config /etc/tailscale2otel/config.yaml
```

### Docker Compose

```sh
CONFIG=examples/config/prometheus-only.yaml
cp "$CONFIG" deploy/config.yaml
COMPOSE_FILES=(-f deploy/docker-compose.yaml -f deploy/docker-compose.config.yaml)
if [[ "$CONFIG" == "examples/config/prometheus-only.yaml" ]]; then
  COMPOSE_FILES+=(-f deploy/docker-compose.prometheus.yaml)
  export TS2OTEL_PROMETHEUS__LISTEN=:2112
  export TS2OTEL_PROMETHEUS__AUTH__ALLOW_UNAUTHENTICATED=true
fi
docker compose "${COMPOSE_FILES[@]}" up
```

Put the matching environment variables in `deploy/.env`; the base Compose file reads that file.

### Kubernetes (Helm)

```sh
CONFIG=examples/config/prometheus-only.yaml
kubectl create configmap tailscale2otel-config --from-file=config.yaml="$CONFIG"
HELM_PROMETHEUS_ARGS=()
if [[ "$CONFIG" == "examples/config/prometheus-only.yaml" ]]; then
  HELM_PROMETHEUS_ARGS=(
    --set-string config.delivery.mode=prometheus
    --set-string config.prometheus.listen=:2112
    --set config.prometheus.auth.allow_unauthenticated=true
    --set-string metrics.externalPrometheusToken=absent
    --set metrics.podMonitor.enabled=true
  )
fi
helm install tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
  --set-string existingConfigMap=tailscale2otel-config \
  --set-string existingSecret=tailscale2otel-creds \
  "${HELM_PROMETHEUS_ARGS[@]}"
```

Create `tailscale2otel-creds` from an env file containing the route's credentials; never put a
credential in Helm `--set`. For the Prometheus starter, put
`TS2OTEL_PROMETHEUS__LISTEN=:2112` and
`TS2OTEL_PROMETHEUS__AUTH__ALLOW_UNAUTHENTICATED=true` in that env file too: `existingConfigMap`
means the chart cannot rewrite the mounted starter, while these env values make the app match the
PodMonitor values above. For a re-run, replace the ConfigMap or use a new name, then roll out.

### Local binary

```sh
CONFIG=examples/config/prometheus-only.yaml
tailscale2otel -config "$CONFIG"
```

For the expected result and verification commands, return to the selected destination above.

## Confirm data is flowing

**Self-observability metric:** `tailscale2otel` emits a `tailscale2otel.up` gauge (normalized to
`tailscale2otel_up` in Prometheus/Grafana) once the first export cycle completes successfully. Query
for it in Grafana Explore — if it appears, the pipeline is working end-to-end.

**Admin status page:** the admin server is on by default and binds `127.0.0.1:9091`, giving live
visibility into collector health without querying the backend. The status page shows each
collector's last-run time, success or failure, active-series cardinality, OTLP delivery state per
signal, the full metrics catalog, and a redacted config summary. In multi-tailnet deployments
(`tailnets:` with more than one entry) a **Tailnet** selector filters the Collectors, API,
Cardinality and device-inventory tabs down to one tailnet, or "All" for the combined view; the
selection round-trips through the URL (`?tailnet=`), so a refresh or a shared link keeps it. Runtime,
OTLP delivery and process-level data are process-global and always cover every tailnet, labelled as
such. The `/healthz` and `/readyz` endpoints are always available without authentication and are
suitable for container health checks.

A loopback default means "loopback **inside the container**", so reaching it from your machine takes
both a published port and a bind the container will accept traffic on — plus a token, because a
non-loopback bind without one is refused:

```sh
docker run -p 9091:9091 \
  -e TS2OTEL_ADMIN__LISTEN=0.0.0.0:9091 \
  -e TS2OTEL_ADMIN__AUTH__TOKEN="$ADMIN_TOKEN" \
  ghcr.io/rknightion/tailscale2otel:latest
```

Then open `http://localhost:9091/` and supply the token as the HTTP Basic password (any username) or
as `Authorization: Bearer <token>`.

!!! warning "The admin page fails closed on a network-reachable bind"
    With **no** `admin.auth.token` the page is served **only** on a loopback `admin.listen`; every
    other bind — including a tailnet address — is refused with HTTP 403. Setting
    `TS2OTEL_ADMIN__LISTEN=:9091` *without* a token therefore does not expose the page, it makes it
    answer 403 to everyone, which reads like a broken exporter. Set
    `TS2OTEL_ADMIN__AUTH__TOKEN` whenever the bind is not loopback. `/healthz` and `/readyz` are
    never gated and stay reachable either way.

## What's collected by default

The standard API collectors are enabled out of the box. `node_metrics` needs targets or discovery,
and `k8s_audit` needs a tsrecorder export, so both are off by default. The polling cadences are:

| Collector | Default interval |
|---|---|
| `devices`, `flowlogs`, `auditlogs`, `node_metrics` | 60 s |
| `users`, `keys`, `oauth_apps` | 300 s |
| `settings`, `acl`, `dns`, `contacts`, `webhooks`, `posture_integrations`, `log_stream`, `services` | 600 s |

Flow and audit logs default to `source: poll` — the exporter pulls them from the Tailscale API.
See [Configuration](configuration.md) for how to switch to the streaming (HEC push) path and for
all the tuning knobs.

## Next steps

- [Installation](installation.md) — Docker Compose, Helm chart, and binary installation with
  persistent checkpoint volumes.
- [Collector Gateway](gateway.md) — put Alloy or an OpenTelemetry Collector between the exporter and
  your backend so a backend outage does not cost you the telemetry produced during it.
- [Configuration](configuration.md) — the complete key-by-key reference, including log streaming,
  the webhook receiver, cardinality controls, and node-metrics scraping.
- [Dashboards](dashboards.md) — import the shipped Grafana dashboards and see the data you just
  started collecting.
- [Why this exporter](comparison.md) — the trade-offs against the Tailscale console and native
  node metrics.
- [FAQ](faq.md) — short operational answers with links to the authoritative detail.

## Get help

If something here did not work, the fastest routes are the
[troubleshooting guide](troubleshooting.md) or the project's
[GitHub issue tracker](https://github.com/rknightion/tailscale2otel/issues).

- [Source code and README](https://github.com/rknightion/tailscale2otel)
- [Latest release and changelog](https://github.com/rknightion/tailscale2otel/releases/latest)
- [`config.example.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml) — the annotated starter config
