---
title: Getting Started
description: From zero to your first tailscale2otel metrics in Grafana Cloud
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

## Smoke test: run with stdout output

Two flags help before any real rollout: `tailscale2otel -version` prints the build version and
exits, and `tailscale2otel -validate -config <path>` loads and validates a config file (the same
load/validate path the server uses) without starting the exporter — useful for pre-rollout config
linting. It prints any advisory warnings, exits 0 if valid, 1 otherwise.

Before pointing the exporter at a real backend, verify it can connect to the Tailscale API and
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
    (or `http`) and point `TS2OTEL_OTLP__ENDPOINT` at your collector's OTLP receiver address.
    `TS2OTEL_OTLP__HEADERS` is a map field and must be set via a config file — see
    [Configuration](configuration.md).

## Confirm data is flowing

**Self-observability metric:** `tailscale2otel` emits a `tailscale2otel.up` gauge (normalized to
`tailscale2otel_up` in Prometheus/Grafana) once the first export cycle completes successfully. Query
for it in Grafana Explore — if it appears, the pipeline is working end-to-end.

**Admin status page:** the admin server is on by default, giving live visibility into collector
health without querying the backend. Override the bind address (or disable it) if you need to:

```sh
-e TS2OTEL_ADMIN__LISTEN=:9091
```

Then open `http://localhost:9091/` in a browser. The status page shows each collector's last-run
time, success or failure, active-series cardinality, the full metrics catalog, and a redacted
config summary. The `/healthz` and `/readyz` endpoints are always available without authentication
and are suitable for container health checks.

!!! warning "Bind the admin server carefully"
    By default the admin page is open (no token required), so bind `admin.listen` to a loopback or
    tailnet address rather than `0.0.0.0`. Set `TS2OTEL_ADMIN__AUTH__TOKEN` to require a shared
    secret for the status page and pprof handlers. `/healthz` and `/readyz` are never gated.

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
- [Configuration](configuration.md) — the complete key-by-key reference, including log streaming,
  the webhook receiver, cardinality controls, and node-metrics scraping.
