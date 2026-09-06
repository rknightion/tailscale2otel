---
title: tailscale2otel — Tailscale OpenTelemetry & Prometheus exporter
description: Export Tailscale device fleet metrics, network flow logs and audit logs to OpenTelemetry (OTLP) or Prometheus. Single Go binary, Grafana Cloud ready, Headscale supported.
image: assets/social-card.png
---

# tailscale2otel

tailscale2otel exports device fleet state, network flows and configuration changes from Tailscale
as OpenTelemetry metrics and logs. Choose OTLP push, Prometheus pull, or both for separate
destinations. It runs as a single static Go binary.

[Headscale](https://headscale.net/) supports a reduced set: devices, users, keys, ACL and node metrics.
Source code and releases are on [GitHub](https://github.com/rknightion/tailscale2otel).

## Quickstart

Choose a destination before collecting credentials: [Grafana Cloud over
OTLP](getting-started.md#grafana-cloud-over-otlp), [Prometheus
pull](getting-started.md#prometheus-pull), or [stdout](getting-started.md#stdout). The
[Getting Started](getting-started.md) page is the canonical command reference for Docker, Compose,
Helm, and a local binary; it states the expected first result for each route.

## Start here

<div class="grid cards" markdown>

- **[Getting started](getting-started.md)** - choose a destination, create Tailscale authentication,
  and reach a first observable signal.
- **[Installation](installation.md)** - Docker, Helm, docker-compose, or a
  prebuilt binary for Linux, macOS and Windows.
- **[Configuration](configuration.md)** - every key, its default, and the `TS2OTEL_*`
  environment variable that overrides it.
- **[Metrics catalog](metrics.md)** - all 332 metrics and 30 log-event types,
  with their OTLP→Prometheus names.

</div>

## What it collects

17 collectors run on independent schedules, each isolated so one failing source cannot stall
the others:

| Area | What you get |
|---|---|
| **Network flow logs** | Throughput, packet and flow counters aggregated for dashboards, plus per-connection records as logs for drill-down. Cardinality is bounded by a top-N rollup, to limit series growth. See [configuration](configuration.md). |
| **Audit logs** | Every tailnet configuration change as a structured log event, plus a security-categorized counter you can alert on. |
| **Device fleet** | Online state, last seen, key and cert expiry, client version skew, NAT and connectivity quality, per-DERP latency, subnet routes, tailnet lock, and hygiene roll-ups. |
| **Identity & access** | Users and roles, auth keys, OAuth clients, API tokens and their expiry, plus outstanding invites. |
| **Policy & posture** | ACL size and change detection with structural risk scoring, DNS configuration, tailnet settings, and MDM/EDR posture integrations. |
| **PAM** | Border0 connector, service, policy and identity inventory, plus session telemetry. See [PAM](pam.md). |
| **Node metrics** | `tailscaled`'s own `:5252` metrics, scraped centrally with automatic target discovery. See [node metrics](node-metrics.md). |

## How the data gets in

Flow and audit logs can enter through three sources that feed the same processors:

1. **Polling** the Tailscale API on a schedule: the default; it needs no
   inbound listener.
2. **Log streaming** - Tailscale pushes flow and audit logs to a built-in Splunk-HEC-compatible
   receiver. Lower latency, but requires an endpoint Tailscale can reach.
3. **Object storage** - tailscale2otel reads the exports Tailscale writes to an S3-compatible
   bucket. This is the durable batch path and supports backfill.

Pick exactly one source per log type. **Webhooks are a separate fourth path for real-time,
HMAC-verified tailnet events**, not an alternative source for flow or audit logs. Details and the
trade-offs are in
[streaming & webhooks](streaming-webhooks.md).

## Where it sends data

OTLP over gRPC or HTTP is the primary path, with Grafana Cloud authentication built in. A separate,
opt-in Prometheus pull endpoint serves the same metrics on its own listener if you would rather
scrape than push. The two can run at once. There is also a `stdout` mode for local debugging
with no backend at all.

Ready-made [dashboards](dashboards.md) and [alert rules](alerts.md) ship with the project.

## Reading further

| | |
|---|---|
| [Feature guide](features.md) | Every user-facing feature and its setup reference |
| [High availability](high-availability.md) | Kubernetes coordination, state and rollouts |
| [Flow view](flow-view.md) | Local traffic queries and exports |
| [Event explorer](events.md) | Recent audit and webhook events |
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
requests are welcome on [GitHub](https://github.com/rknightion/tailscale2otel) - see the
[open issues](https://github.com/rknightion/tailscale2otel/issues) or the
[latest release](https://github.com/rknightion/tailscale2otel/releases/latest).
