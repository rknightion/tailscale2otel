---
title: tailscale2otel — Tailscale OpenTelemetry & Prometheus exporter
description: Export Tailscale device fleet metrics, network flow logs and audit logs to OpenTelemetry (OTLP) or Prometheus. Single Go binary, Grafana Cloud ready, Headscale supported.
image: assets/social-card.png
---

# tailscale2otel

**Turn a Tailscale tailnet into observability data.** `tailscale2otel` is a single static Go binary
that reads everything Tailscale's API will tell you about your network — who is online, what they
are talking to, what changed, and what is about to expire — and emits it as OpenTelemetry metrics
and logs over OTLP, a Prometheus `/metrics` endpoint, or both simultaneously.

It is built for people who already run Grafana Cloud, Alloy, or any OTEL collector and want their
tailnet to show up there alongside everything else, rather than living in a separate admin console.
[Headscale](https://headscale.net/) users get a supported subset against a self-hosted control plane:
devices, users, keys, ACL, and node metrics.

The source, releases and issue tracker live on
**[GitHub](https://github.com/rknightion/tailscale2otel)**.

## Quickstart

Needs a Tailscale OAuth client, and a Grafana Cloud stack if you want the data to
land somewhere:

```sh
docker run --rm --stop-timeout 45 \
  -e TS2OTEL_TAILSCALE__TAILNET=example.com \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id> \
  -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id> \
  -e TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token> \
  ghcr.io/rknightion/tailscale2otel:latest
```

No config file is needed for the common settings: scalar fields and simple lists have `TS2OTEL_*`
environment variables. Maps and structured lists stay in YAML. To try it with no backend at all,
set `TS2OTEL_OTLP__PROTOCOL=stdout` and it prints to the console.

## Start here

<div class="grid cards" markdown>

- **[Getting started](getting-started.md)** — from zero to metrics landing in
  Grafana Cloud, including creating the OAuth client.
- **[Installation](installation.md)** — Docker, Helm, docker-compose, or a
  prebuilt binary for Linux, macOS and Windows.
- **[Configuration](configuration.md)** — every key, its default, and the `TS2OTEL_*`
  environment variable that overrides it.
- **[Metrics catalog](metrics.md)** — all 293 metrics and 17 log-event types,
  with their OTLP→Prometheus names.

</div>

## What it collects

Sixteen collectors run on independent schedules, each isolated so one failing source cannot stall
the others:

| Area | What you get |
|---|---|
| **Network flow logs** | Throughput, packet and flow counters aggregated for dashboards, plus per-connection records as logs for drill-down. Cardinality is bounded by a top-N rollup, so this stays affordable. See [configuration](configuration.md). |
| **Audit logs** | Every tailnet configuration change as a structured log event, plus a security-categorized counter you can alert on. |
| **Device fleet** | Online state, last seen, key and cert expiry, client version skew, NAT and connectivity quality, per-DERP latency, subnet routes, tailnet lock, and hygiene roll-ups. |
| **Identity & access** | Users and roles, auth keys, OAuth clients, API tokens and their expiry, plus outstanding invites. |
| **Policy & posture** | ACL size and change detection with structural risk scoring, DNS configuration, tailnet settings, and MDM/EDR posture integrations. |
| **Node metrics** | `tailscaled`'s own `:5252` metrics, scraped centrally with automatic target discovery. See [node metrics](node-metrics.md). |

## How the data gets in

Flow and audit logs can enter through three sources that feed the same processors:

1. **Polling** the Tailscale API on a schedule — the default, and the only option that needs no
   inbound network exposure.
2. **Log streaming** — Tailscale pushes flow and audit logs to a built-in Splunk-HEC-compatible
   receiver. Lower latency, but requires an endpoint Tailscale can reach.
3. **Object storage** — tailscale2otel reads the exports Tailscale writes to an S3-compatible
   bucket. This is the durable batch path and supports backfill.

Pick exactly one source per log type. **Webhooks are a separate fourth path for real-time,
HMAC-verified tailnet events**, not an alternative source for flow or audit logs. Details and the
trade-offs are in
[streaming & webhooks](streaming-webhooks.md).

## Where it sends data

OTLP over gRPC or HTTP is the primary path, with Grafana Cloud authentication built in. A separate,
opt-in Prometheus pull endpoint serves the same metrics on its own listener if you would rather
scrape than push — and the two can run at once. There is also a `stdout` mode for local debugging
with no backend at all.

Ready-made [dashboards](dashboards.md) and [alert rules](alerts.md) ship with the project.

## Reading further

| | |
|---|---|
| [Architecture](architecture.md) | How collectors, processors and the OTEL facade fit together |
| [Node metrics](node-metrics.md) | Central `tailscaled` scraping and target discovery |
| [Streaming & webhooks](streaming-webhooks.md) | Receiver setup, auth, and `auto_configure` |
| [Environment variables](env-vars.md) | The complete generated `TS2OTEL_*` reference |
| [Security](security.md) | Data handling, PII redaction, receiver authentication |
| [Upgrading](upgrading.md) | Version-to-version migration notes |
| [Troubleshooting](troubleshooting.md) | Common failure modes and how to diagnose them |
| [Runbooks](runbooks.md) | Alert investigation and remediation |
| [Kubernetes audit](kubernetes-audit.md) | Optional tsrecorder audit ingestion and signals |

## Project

tailscale2otel is open source under the Apache 2.0 licence. Bug reports, feature requests and pull
requests are welcome on [GitHub](https://github.com/rknightion/tailscale2otel) — see the
[open issues](https://github.com/rknightion/tailscale2otel/issues) or the
[latest release](https://github.com/rknightion/tailscale2otel/releases/latest).
