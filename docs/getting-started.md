---
title: Getting Started
description: Configure Tailscale API access, run tailscale2otel, and send your first metrics and logs to Grafana Cloud over OTLP.
tags:
  - Getting Started
  - Grafana Cloud
---

# Getting Started

This guide walks you through getting `tailscale2otel` running and sending your first Tailscale
metrics and logs to Grafana Cloud.

## Prerequisites

Before you begin, you need:

- A **Tailscale tailnet** you control.
- **Authentication credentials** — an OAuth client is strongly preferred:
    - **OAuth client (recommended):** create one in the [Tailscale admin
      console](https://tailscale.com/kb/1215/oauth-clients) with the least-privilege read scopes
      your collectors need (at minimum `all:read`). OAuth tokens are short-lived, auto-refreshed,
      and not tied to a user account.
    - **API key (fallback):** a personal API key also works (`method: apikey`), but it expires in
      90 days or less and is tied to its creator — the exporter logs a warning when one is
      configured.
- A **Grafana Cloud stack** with an OTLP endpoint and a token. From your stack's **Connections →
  OpenTelemetry** page, note your OTLP gateway URL (format:
  `https://otlp-gateway-<region>.grafana.net/otlp`), your stack/instance ID, and generate an
  access-policy token with `metrics:write` and `logs:write` scopes. If you enable the optional
  traces pillar (`tracing.enabled: true`), add `traces:write` to the token as well.

!!! tip "Running Headscale instead of Tailscale?"
    tailscale2otel also supports a self-hosted [Headscale](https://headscale.net/) control plane —
    set `provider: headscale` and point it at your server instead of the steps below. A reduced
    collector set runs automatically (devices, users, keys, ACL, node-metrics); see
    [Configuration → `headscale`](configuration.md#headscale-headscale-control-plane-connection)
    for the connection settings and exactly what's affected.

## Minimal env-only configuration

The config file is entirely optional — `tailscale2otel` runs from built-in defaults plus
environment variables. The minimum set to pass the exporter your tailnet and Grafana Cloud
destination is:

```sh
TS2OTEL_TAILSCALE__TAILNET=example.com           # or leave as "-" for the auth principal's default tailnet
TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id>
TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret>
TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id>
TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token>
```

These map to the config keys `tailscale.tailnet`, `tailscale.auth.oauth.client_id`,
`tailscale.auth.oauth.client_secret`, `otlp.grafana_cloud.instance_id`, and
`otlp.grafana_cloud.token`. The `TS2OTEL_` prefix + `__` between nesting levels is the universal
convention — see [Configuration](configuration.md) for the full mapping rules.

!!! tip "Secrets belong in env vars"
    Keep tokens and client secrets in environment variables only. They never need to appear in a
    YAML file, and a YAML file committed to version control is an easy way to leak credentials.

## Smoke test: prove it works before any real rollout

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

Before pointing the exporter at a real backend, you can also verify it can connect to the Tailscale API and
format telemetry by printing to stdout instead:

```sh
docker build -f deploy/Dockerfile -t tailscale2otel .
docker run --rm \
  -e TS2OTEL_TAILSCALE__TAILNET=example.com \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id> \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret> \
  -e TS2OTEL_OTLP__PROTOCOL=stdout \
  tailscale2otel
```

Setting `TS2OTEL_OTLP__PROTOCOL=stdout` (or `otlp.protocol: stdout` in YAML) prints metrics and
logs to the console — no OTLP backend required. You should see device gauge lines appear within the
first polling interval (default 60 seconds). If the Tailscale API connection fails you will see an
error in the log output instead.

## Point it at Grafana Cloud

Once the smoke test produces output, switch to HTTP OTLP and supply your Grafana Cloud credentials.
The `grafana_cloud` convenience block builds the `Authorization: Basic` header for you from the
instance ID and token:

```sh
docker run --rm \
  -e TS2OTEL_TAILSCALE__TAILNET=example.com \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id> \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token> \
  tailscale2otel
```

The exporter defaults to `otlp.protocol: http` and the example endpoint
`https://otlp-gateway-prod-us-central-0.grafana.net/otlp`. If your stack is in a different region,
override the endpoint:

```sh
-e TS2OTEL_OTLP__ENDPOINT=https://otlp-gateway-eu-west-0.grafana.net/otlp
```

!!! note "Self-hosted Collector or Alloy"
    For a self-hosted OpenTelemetry Collector or Grafana Alloy, set `TS2OTEL_OTLP__PROTOCOL=grpc`
    (or `http`) and point `TS2OTEL_OTLP__ENDPOINT` at your collector's OTLP receiver address — for
    a gateway reachable as `alloy` on the same Docker network or as a Kubernetes Service:

    ```sh
    -e TS2OTEL_OTLP__PROTOCOL=grpc \
    -e TS2OTEL_OTLP__ENDPOINT=http://alloy:4317 \
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

All collectors are enabled out of the box except `node_metrics` (which requires explicit target
configuration). The polling cadences are:

| Collector | Default interval |
|---|---|
| `devices`, `flowlogs`, `auditlogs`, `node_metrics` | 60 s |
| `users`, `keys` | 300 s |
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

## Get help

If something here did not work, the fastest routes are the
[troubleshooting guide](troubleshooting.md) or the project's
[GitHub issue tracker](https://github.com/rknightion/tailscale2otel/issues).

- [Source code and README](https://github.com/rknightion/tailscale2otel)
- [Latest release and changelog](https://github.com/rknightion/tailscale2otel/releases/latest)
- [`config.example.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml) — the annotated starter config
